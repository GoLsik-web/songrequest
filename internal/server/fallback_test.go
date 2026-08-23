package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"songrequest/internal/config"
	"songrequest/internal/spotify"
)

func decodeBody(t *testing.T, r *http.Request, into *map[string]any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		t.Fatalf("не разобрал тело запроса: %v", err)
	}
}

// Запасной плейлист включался списком из десяти треков. В Spotify это
// выглядит так, будто музыка играет ниоткуда, и обрывается на десятом треке.
// Включать надо сам плейлист — тогда Spotify играет его дальше сам.
func TestFallbackPlaylistPlaysAsPlaylist(t *testing.T) {
	var playBody map[string]any

	srv, _ := newTestServer(t,
		func(c *config.Config) {
			c.ResumeFail = config.ResumeFallbackPlaylist
			c.FallbackPlaylistID = "3ZJzqmB44S2VQflmFjjBsS"
			c.FallbackPlaylist = "Фон для стрима"
		},
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/me/player/play" {
				decodeBody(t, r, &playBody)
			}
			w.WriteHeader(http.StatusNoContent)
		})

	srv.afterRestore(context.Background(),
		&spotify.Snapshot{Empty: true},
		spotify.RestoreOutcome{Code: "SP-12"})

	if playBody == nil {
		t.Fatal("запасной плейлист не заиграл")
	}
	if playBody["context_uri"] != "spotify:playlist:3ZJzqmB44S2VQflmFjjBsS" {
		t.Fatalf("плейлист должен включаться целиком, а ушло: %v", playBody)
	}
	if playBody["uris"] != nil {
		t.Fatalf("список треков вместо плейлиста — это и была ошибка: %v", playBody["uris"])
	}
	if text := lastNoticeText(srv); !strings.Contains(text, "Фон для стрима") {
		t.Fatalf("в панели должно быть видно, что именно заиграло: %q", text)
	}
}

// То же для режима «музыка последнего артиста»: контекст артиста Spotify
// играет сам, список из десяти треков — нет.
func TestArtistRadioPlaysAsArtistContext(t *testing.T) {
	var playBody map[string]any

	srv, _ := newTestServer(t,
		func(c *config.Config) { c.ResumeFail = config.ResumeArtistRadio },
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/me/player/play" {
				decodeBody(t, r, &playBody)
			}
			w.WriteHeader(http.StatusNoContent)
		})

	srv.afterRestore(context.Background(),
		&spotify.Snapshot{Empty: true, ArtistID: "0TnOYISbd1XYRBk9myaseg", ArtistName: "Кино"},
		spotify.RestoreOutcome{Code: "SP-12"})

	if playBody == nil || playBody["context_uri"] != "spotify:artist:0TnOYISbd1XYRBk9myaseg" {
		t.Fatalf("ждали контекст артиста, ушло: %v", playBody)
	}
}

// Возврат без источника: трек вернули, а продолжать нечем — очередь Spotify
// надо дозаполнить, иначе после одного трека наступит тишина.
func TestRestoreWithoutSourceStillGetsContinuation(t *testing.T) {
	srv, calls := newTestServer(t,
		func(c *config.Config) {
			c.ResumeFail = config.ResumeFallbackPlaylist
			c.FallbackPlaylistID = "плейлист"
			c.FallbackPlaylist = "Фон"
		},
		func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/playlists/") {
				writeTestJSON(w, map[string]any{"items": []any{
					map[string]any{"track": map[string]any{"uri": "spotify:track:раз"}},
					map[string]any{"track": map[string]any{"uri": "spotify:track:два"}},
				}})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})

	// Снимок без источника — ровно то, что Spotify отдаёт после автоподбора
	// или когда трек включили из поиска.
	srv.afterRestore(context.Background(),
		&spotify.Snapshot{TrackURI: "spotify:track:вернули", ContextURI: ""},
		spotify.RestoreOutcome{Restored: true, ContextLost: true, DeviceID: "комп"})

	if len(queuedTracks(*calls)) == 0 {
		t.Fatal("после одиночного трека музыка оборвётся — очередь надо дозаполнить")
	}
}
