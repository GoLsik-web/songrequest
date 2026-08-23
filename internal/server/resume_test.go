package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"songrequest/internal/app"
	"songrequest/internal/config"
	"songrequest/internal/logx"
	"songrequest/internal/spotify"
	"songrequest/internal/store"
	"songrequest/internal/twitch"
)

// Поведение при невозможности вернуть контекст — настройка, которую стример
// выставляет один раз и проверяет раз в жизни, поэтому она должна быть
// покрыта тестами целиком: ошибку здесь заметят не сразу.

type fakeSecrets struct {
	mu   sync.Mutex
	data map[string]string
}

func (f *fakeSecrets) PutJSON(name string, v any) error {
	b, _ := json.Marshal(v)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.data == nil {
		f.data = map[string]string{}
	}
	f.data[name] = string(b)
	return nil
}

func (f *fakeSecrets) GetJSON(name string, v any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, ok := f.data[name]
	if !ok {
		return io.EOF
	}
	return json.Unmarshal([]byte(raw), v)
}

func (f *fakeSecrets) Delete(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, name)
	return nil
}

type recorded struct {
	Method string
	Path   string
	Query  string
}

func newTestServer(t *testing.T, tune func(*config.Config), handler http.HandlerFunc) (*Server, *[]recorded) {
	t.Helper()

	var (
		mu    sync.Mutex
		calls []recorded
	)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, recorded{r.Method, r.URL.Path, r.URL.RawQuery})
		mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(api.Close)

	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if tune != nil {
		cfg.Update(tune)
	}

	log, err := logx.New(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })

	keys := &fakeSecrets{}
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	srv := &Server{
		state:   app.New("тест"),
		cfg:     cfg,
		log:     log,
		spotify: spotify.NewForTest(cfg, log, keys, api.URL),
		twitch:  twitch.NewForTest(cfg, log, keys, api.URL),
		db:      db,
		dataDir: dir,
	}
	return srv, &calls
}

func queuedTracks(calls []recorded) []string {
	var out []string
	for _, c := range calls {
		if c.Path == "/me/player/queue" && c.Method == http.MethodPost {
			out = append(out, c.Query)
		}
	}
	return out
}

// Возврат прошёл полностью — трогать чужой плеер дальше нельзя ни в каком режиме.
func TestFullRestoreLeavesPlayerAlone(t *testing.T) {
	srv, calls := newTestServer(t,
		func(c *config.Config) {
			c.ResumeFail = config.ResumeArtistRadio
		},
		func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("лишний запрос: %s %s", r.Method, r.URL.Path)
		})

	srv.afterRestore(context.Background(),
		&spotify.Snapshot{TrackURI: "spotify:track:x", ArtistID: "art"},
		spotify.RestoreOutcome{Restored: true, ContextLost: false})

	if len(*calls) != 0 {
		t.Fatalf("после полного возврата в Spotify ходить незачем, а сходили %d раз", len(*calls))
	}
}

// Контекст потерян: трек вернули, дальше должно заиграть радио артиста.
func TestContextLostFillsWithArtistRadio(t *testing.T) {
	srv, calls := newTestServer(t,
		func(c *config.Config) {
			c.ResumeFail = config.ResumeArtistRadio
		},
		func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/artists/") {
				writeTestJSON(w, map[string]any{"tracks": []any{
					map[string]any{"uri": "spotify:track:вернули"}, // он уже играет
					map[string]any{"uri": "spotify:track:второй"},
					map[string]any{"uri": "spotify:track:третий"},
				}})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})

	srv.afterRestore(context.Background(),
		&spotify.Snapshot{TrackURI: "spotify:track:вернули", ArtistID: "art", ArtistName: "Кино"},
		spotify.RestoreOutcome{Restored: true, ContextLost: true, DeviceID: "комп"})

	queued := queuedTracks(*calls)
	if len(queued) != 2 {
		t.Fatalf("ждали два трека в очереди, получили %d: %v", len(queued), queued)
	}
	for _, q := range queued {
		if strings.Contains(q, "вернули") {
			t.Fatal("трек, который уже играет, ставить в очередь второй раз нельзя")
		}
	}
	if !strings.Contains(lastNoticeText(srv), "Кино") {
		t.Fatalf("панель должна объяснить, почему заиграло именно это: %q", lastNoticeText(srv))
	}
}

// Режим «ничего не включать» — честная тишина плюс предупреждение.
func TestContextLostWithNothingModeOnlyWarns(t *testing.T) {
	srv, calls := newTestServer(t,
		func(c *config.Config) { c.ResumeFail = config.ResumeNothing },
		func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("лишний запрос: %s %s", r.Method, r.URL.Path)
		})

	srv.afterRestore(context.Background(),
		&spotify.Snapshot{TrackURI: "spotify:track:x", ArtistID: "art"},
		spotify.RestoreOutcome{Restored: true, ContextLost: true})

	if len(*calls) != 0 {
		t.Fatal("в режиме «ничего не включать» приложение не должно трогать плеер")
	}
	if text := lastNoticeText(srv); !strings.Contains(text, "остановится") {
		t.Fatalf("стример должен быть предупреждён о тишине впереди: %q", text)
	}
}

// Возвращать было нечего — включаем запасной плейлист с нуля.
func TestNothingToRestoreStartsFallbackPlaylist(t *testing.T) {
	var played bool

	srv, _ := newTestServer(t,
		func(c *config.Config) {
			c.ResumeFail = config.ResumeFallbackPlaylist
			c.FallbackPlaylistID = "37i9dQZF1DXcBWIGoYBM5M"
			c.FallbackPlaylist = "Фон для стрима"
		},
		func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasPrefix(r.URL.Path, "/playlists/"):
				writeTestJSON(w, map[string]any{"items": []any{
					map[string]any{"track": map[string]any{"uri": "spotify:track:первый"}},
					map[string]any{"track": map[string]any{"uri": "spotify:track:локальный", "is_local": true}},
					map[string]any{"track": map[string]any{"uri": "spotify:track:второй"}},
				}})
			case r.URL.Path == "/me/player/play":
				played = true
				w.WriteHeader(http.StatusNoContent)
			default:
				w.WriteHeader(http.StatusNoContent)
			}
		})

	srv.afterRestore(context.Background(),
		&spotify.Snapshot{Empty: true},
		spotify.RestoreOutcome{Code: "SP-12"})

	if !played {
		t.Fatal("запасной плейлист должен был заиграть")
	}
	if text := lastNoticeText(srv); !strings.Contains(text, "Фон для стрима") {
		t.Fatalf("в панели должно быть видно название плейлиста: %q", text)
	}
}

// Выбран режим «запасной плейлист», но сам плейлист не выбран — молча
// деградируем до тишины, а не пытаемся играть пустоту.
func TestFallbackPlaylistWithoutPlaylistDegradesToSilence(t *testing.T) {
	srv, calls := newTestServer(t,
		func(c *config.Config) {
			c.ResumeFail = config.ResumeFallbackPlaylist
			c.FallbackPlaylistID = ""
		},
		func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("лишний запрос: %s %s", r.Method, r.URL.Path)
		})

	srv.afterRestore(context.Background(),
		&spotify.Snapshot{Empty: true},
		spotify.RestoreOutcome{Code: "SP-12"})

	if len(*calls) != 0 {
		t.Fatal("без выбранного плейлиста включать нечего")
	}

	cfg := srv.cfg.Get()
	if !cfg.ResumeModeIncomplete() {
		t.Fatal("панель должна знать, что режим настроен не до конца")
	}
	if cfg.EffectiveResumeMode() != config.ResumeNothing {
		t.Fatalf("ждали деградацию до тишины, получили %q", cfg.EffectiveResumeMode())
	}
}

// Радио артиста без известного артиста — тоже не повод падать.
func TestArtistRadioWithoutArtistReportsClearly(t *testing.T) {
	srv, _ := newTestServer(t,
		func(c *config.Config) { c.ResumeFail = config.ResumeArtistRadio },
		func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("лишний запрос: %s %s", r.Method, r.URL.Path)
		})

	srv.afterRestore(context.Background(),
		&spotify.Snapshot{Empty: true},
		spotify.RestoreOutcome{Code: "SP-12"})

	if text := lastNoticeText(srv); !strings.Contains(text, "артиста") {
		t.Fatalf("причина должна быть понятна из панели: %q", text)
	}
}

func lastNoticeText(s *Server) string {
	notices := s.state.Snapshot().Notices
	if len(notices) == 0 {
		return ""
	}
	return notices[len(notices)-1].Text
}

func writeTestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
