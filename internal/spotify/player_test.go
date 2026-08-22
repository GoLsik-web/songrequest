package spotify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"songrequest/internal/config"
	"songrequest/internal/errs"
	"songrequest/internal/logx"
)

// fakeSecrets заменяет хранилище учётных данных Windows: тесты не должны
// трогать настоящий «Диспетчер учётных данных».
type fakeSecrets struct{ data map[string]string }

func (f *fakeSecrets) PutJSON(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if f.data == nil {
		f.data = map[string]string{}
	}
	f.data[name] = string(b)
	return nil
}

func (f *fakeSecrets) GetJSON(name string, v any) error {
	raw, ok := f.data[name]
	if !ok {
		return errNotFoundForTest
	}
	return json.Unmarshal([]byte(raw), v)
}

func (f *fakeSecrets) Delete(name string) error {
	delete(f.data, name)
	return nil
}

var errNotFoundForTest = io.EOF // подойдёт любая ошибка: клиент её просто логирует

// call — запись об одном запросе, дошедшем до поддельного Spotify.
type call struct {
	Method string
	Path   string
	Query  string
	Body   map[string]any
}

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *[]call, *httptest.Server) {
	t.Helper()

	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			json.Unmarshal(raw, &body)
		}
		calls = append(calls, call{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: body})
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	log, err := logx.New(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })

	c := New(cfg, log, &fakeSecrets{})
	c.apiBase = srv.URL
	c.tokenURL = srv.URL + "/api/token"
	// Считаем, что вход уже выполнен и ключ доступа свежий.
	c.tokens = tokens{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	return c, &calls, srv
}

func TestBuildPlayBody(t *testing.T) {
	tests := []struct {
		name        string
		snap        Snapshot
		wantContext string
		wantURIs    []string
		wantOffset  string
		wantLost    bool
	}{
		{
			name: "плейлист возвращается вместе с треком и позицией",
			snap: Snapshot{
				ContextURI: "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M",
				TrackURI:   "spotify:track:11dFghVXANMlKmJXsNCbNl",
				PositionMs: 91000,
			},
			wantContext: "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M",
			wantOffset:  "spotify:track:11dFghVXANMlKmJXsNCbNl",
		},
		{
			name: "альбом тоже умеет начинать с нужного трека",
			snap: Snapshot{
				ContextURI: "spotify:album:4aawyAB9vmqN3uQ7FjRGTy",
				TrackURI:   "spotify:track:11dFghVXANMlKmJXsNCbNl",
				PositionMs: 5000,
			},
			wantContext: "spotify:album:4aawyAB9vmqN3uQ7FjRGTy",
			wantOffset:  "spotify:track:11dFghVXANMlKmJXsNCbNl",
		},
		{
			name: "радио артиста не умеет — включаем сам трек и честно говорим об этом",
			snap: Snapshot{
				ContextURI: "spotify:artist:0TnOYISbd1XYRBk9myaseg",
				TrackURI:   "spotify:track:11dFghVXANMlKmJXsNCbNl",
				PositionMs: 30000,
			},
			wantURIs: []string{"spotify:track:11dFghVXANMlKmJXsNCbNl"},
			wantLost: true,
		},
		{
			name: "без контекста играем один трек и ничего не теряем",
			snap: Snapshot{
				TrackURI:   "spotify:track:11dFghVXANMlKmJXsNCbNl",
				PositionMs: 1000,
			},
			wantURIs: []string{"spotify:track:11dFghVXANMlKmJXsNCbNl"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, lost := BuildPlayBody(&tc.snap)
			if lost != tc.wantLost {
				t.Fatalf("признак потери контекста: получили %v, ждали %v", lost, tc.wantLost)
			}

			pb := body.(*playBody)
			if pb.ContextURI != tc.wantContext {
				t.Errorf("context_uri: получили %q, ждали %q", pb.ContextURI, tc.wantContext)
			}
			if pb.PositionMs != tc.snap.PositionMs {
				t.Errorf("позиция потерялась: получили %d, ждали %d", pb.PositionMs, tc.snap.PositionMs)
			}
			if tc.wantOffset != "" {
				if pb.Offset == nil || pb.Offset.URI != tc.wantOffset {
					t.Errorf("offset: получили %+v, ждали %q", pb.Offset, tc.wantOffset)
				}
			}
			if len(tc.wantURIs) > 0 {
				if len(pb.URIs) != 1 || pb.URIs[0] != tc.wantURIs[0] {
					t.Errorf("uris: получили %v, ждали %v", pb.URIs, tc.wantURIs)
				}
			}
		})
	}
}

func TestChooseDevice(t *testing.T) {
	snap := &Snapshot{DeviceID: "было-тут"}

	tests := []struct {
		name    string
		devices []Device
		want    string
		wantErr errs.Code
	}{
		{
			name: "то же устройство предпочтительнее активного",
			devices: []Device{
				{ID: "другое", IsActive: true},
				{ID: "было-тут"},
			},
			want: "было-тут",
		},
		{
			name: "устройство уснуло — берём активное",
			devices: []Device{
				{ID: "телефон", IsActive: true},
				{ID: "колонка"},
			},
			want: "телефон",
		},
		{
			name:    "активных нет — берём любое управляемое",
			devices: []Device{{ID: "колонка"}},
			want:    "колонка",
		},
		{
			name:    "устройств нет вообще",
			devices: nil,
			wantErr: errs.SpotifyNoDevice,
		},
		{
			name:    "все устройства запрещают управление",
			devices: []Device{{ID: "телевизор", IsRestricted: true, IsActive: true}},
			wantErr: errs.SpotifyNoDevice,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ChooseDevice(snap, tc.devices)
			if tc.wantErr != "" {
				if errs.CodeOf(err) != tc.wantErr {
					t.Fatalf("ждали ошибку %s, получили %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if got != tc.want {
				t.Fatalf("выбрали %q, ждали %q", got, tc.want)
			}
		})
	}
}

func TestIsStale(t *testing.T) {
	snap := &Snapshot{TrackURI: "spotify:track:исходный"}

	tests := []struct {
		name       string
		currentURI string
		playedURI  string
		want       bool
	}{
		{"играет наш заказ — всё по плану", "spotify:track:заказ", "spotify:track:заказ", false},
		{"играет исходный трек — уже вернулись сами", "spotify:track:исходный", "spotify:track:заказ", false},
		{"стример включил третий трек — не лезем", "spotify:track:посторонний", "spotify:track:заказ", true},
		{"плеер молчит — снимок годен", "", "spotify:track:заказ", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsStale(snap, tc.currentURI, tc.playedURI); got != tc.want {
				t.Fatalf("получили %v, ждали %v", got, tc.want)
			}
		})
	}
}

func TestCaptureEmptyPlayerIsNotAnError(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent) // Spotify закрыт — обычное дело
	})

	snap, err := c.Capture(context.Background())
	if err != nil {
		t.Fatalf("тишина не должна быть ошибкой: %v", err)
	}
	if !snap.Empty {
		t.Fatal("снимок должен быть помечен как пустой")
	}
}

func TestCaptureAndRestoreRoundTrip(t *testing.T) {
	const (
		trackURI   = "spotify:track:11dFghVXANMlKmJXsNCbNl"
		contextURI = "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M"
		deviceID   = "комп-стримера"
	)

	// Устройство «засыпает» после заказа: сначала активно, потом нет.
	deviceAwake := true

	c, calls, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/me/player" && r.Method == http.MethodGet:
			if !deviceAwake {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			writeJSON(w, map[string]any{
				"device":        map[string]any{"id": deviceID, "name": "Комп", "is_active": true},
				"repeat_state":  "context",
				"shuffle_state": true,
				"context":       map[string]any{"type": "playlist", "uri": contextURI},
				"progress_ms":   91000,
				"is_playing":    true,
				"item": map[string]any{
					"uri": trackURI, "id": "11dFghVXANMlKmJXsNCbNl",
					"name": "Bohemian Rhapsody", "duration_ms": 354000,
					"artists": []any{map[string]any{"name": "Queen"}},
				},
			})
		case r.URL.Path == "/me/player/devices":
			writeJSON(w, map[string]any{"devices": []any{
				map[string]any{"id": deviceID, "name": "Комп", "is_active": deviceAwake},
			}})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})

	ctx := context.Background()

	snap, err := c.Capture(ctx)
	if err != nil {
		t.Fatalf("снимок не снялся: %v", err)
	}
	if snap.TrackName != "Bohemian Rhapsody" || snap.PositionMs != 91000 ||
		snap.ContextURI != contextURI || !snap.Shuffle || snap.Repeat != "context" {
		t.Fatalf("снимок потерял данные: %+v", snap)
	}

	// Пока играл заказ, устройство Spotify Connect уснуло.
	deviceAwake = false
	*calls = nil

	outcome, err := c.Restore(ctx, snap, "spotify:track:заказ")
	if err != nil {
		t.Fatalf("возврат не удался: %v", err)
	}
	if !outcome.Restored {
		t.Fatalf("возврат не состоялся: %+v", outcome)
	}

	// Проверяем не только факт, но и порядок: сначала разбудить устройство,
	// потом выставить шаффл с повтором, и только затем включать музыку.
	var order []string
	var play *call
	for i := range *calls {
		got := (*calls)[i]
		switch {
		case got.Path == "/me/player" && got.Method == http.MethodPut:
			order = append(order, "перенос")
		case got.Path == "/me/player/shuffle":
			order = append(order, "шаффл")
		case got.Path == "/me/player/repeat":
			order = append(order, "повтор")
		case got.Path == "/me/player/play":
			order = append(order, "старт")
			play = &(*calls)[i]
		}
	}

	want := []string{"перенос", "шаффл", "повтор", "старт"}
	if len(order) != len(want) {
		t.Fatalf("шаги возврата: получили %v, ждали %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("порядок шагов: получили %v, ждали %v", order, want)
		}
	}

	if play == nil {
		t.Fatal("не нашёл запрос на запуск воспроизведения")
	}
	if play.Body["context_uri"] != contextURI {
		t.Errorf("вернулись не в тот плейлист: %v", play.Body["context_uri"])
	}
	if got := play.Body["position_ms"]; got != float64(91000) {
		t.Errorf("вернулись не на ту секунду: %v", got)
	}
	offset, _ := play.Body["offset"].(map[string]any)
	if offset == nil || offset["uri"] != trackURI {
		t.Errorf("вернулись не к тому треку: %v", play.Body["offset"])
	}
}

func TestRestoreSkippedWhenStreamerSwitchedTrackManually(t *testing.T) {
	c, calls, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/me/player" && r.Method == http.MethodGet {
			writeJSON(w, map[string]any{
				"device":     map[string]any{"id": "комп", "is_active": true},
				"is_playing": true,
				"item":       map[string]any{"uri": "spotify:track:совсем-другое", "name": "Что-то своё"},
			})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	snap := &Snapshot{TrackURI: "spotify:track:исходный", TrackName: "Исходный", DeviceID: "комп"}

	outcome, err := c.Restore(context.Background(), snap, "spotify:track:заказ")
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if outcome.Restored {
		t.Fatal("нельзя перебивать музыку, которую стример включил руками")
	}
	if outcome.Code != errs.SpotifyStale {
		t.Fatalf("ждали код %s, получили %s", errs.SpotifyStale, outcome.Code)
	}
	for _, got := range *calls {
		if got.Method == http.MethodPut {
			t.Fatalf("в плеер лезть не должны были, а сходили: %s %s", got.Method, got.Path)
		}
	}
}

func TestRestoreNothingToRestore(t *testing.T) {
	c, calls, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	outcome, err := c.Restore(context.Background(), &Snapshot{Empty: true}, "")
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if outcome.Restored || outcome.Code != errs.SpotifyNothing {
		t.Fatalf("ждали «возвращать нечего», получили %+v", outcome)
	}
	if len(*calls) != 0 {
		t.Fatalf("к Spotify ходить было незачем, а сходили %d раз", len(*calls))
	}
}

func TestRestorePausedSnapshotEndsPaused(t *testing.T) {
	c, calls, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me/player":
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		case "/me/player/devices":
			writeJSON(w, map[string]any{"devices": []any{
				map[string]any{"id": "комп", "is_active": true},
			}})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	snap := &Snapshot{
		DeviceID:   "комп",
		TrackURI:   "spotify:track:исходный",
		PositionMs: 42000,
		IsPlaying:  false, // стример слушал на паузе
	}

	if _, err := c.Restore(context.Background(), snap, ""); err != nil {
		t.Fatalf("возврат не удался: %v", err)
	}

	var paused bool
	for _, got := range *calls {
		if got.Path == "/me/player/pause" {
			paused = true
		}
	}
	if !paused {
		t.Fatal("стример был на паузе — после возврата музыка не должна играть")
	}
}

func TestRestoreWithoutDevicesGivesReadableError(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/me/player/devices" {
			writeJSON(w, map[string]any{"devices": []any{}})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	snap := &Snapshot{TrackURI: "spotify:track:исходный", DeviceID: "исчез"}

	_, err := c.Restore(context.Background(), snap, "")
	if errs.CodeOf(err) != errs.SpotifyNoDevice {
		t.Fatalf("ждали код %s, получили %v", errs.SpotifyNoDevice, err)
	}

	var e *errs.Error
	if !errs.As(err, &e) {
		t.Fatal("ошибка должна нести код для панели")
	}
	if e.Message == "" || len(e.Message) < 20 {
		t.Fatalf("текст ошибки должен объяснять человеку, что делать: %q", e.Message)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
