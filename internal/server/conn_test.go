package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"songrequest/internal/app"
	"songrequest/internal/config"
)

// «Вход есть, но связи не было» — тупик: человек может сказать мне по
// телефону только «не работает». Лампочка обязана называть код.
func TestFailedCheckShowsCodeNotShrug(t *testing.T) {
	srv, _ := newTestServer(t, withClientID, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/me" {
			// Именно так Spotify отвечает, когда у тестера отвалился VPN:
			// вход при этом остаётся живым, и разлогинивать человека нельзя —
			// а вот сказать ему код обязательно.
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":{"status":403,"message":"Spotify is unavailable in this country"}}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	srv.spotify.CheckAccount(context.Background())
	srv.syncSpotifyInfo()

	note := connNote(t, srv, "Spotify")
	if strings.Contains(note, "связи не было") {
		t.Fatalf("лампочка разводит руками вместо кода: %q", note)
	}
	if !strings.HasPrefix(note, "SP-") {
		t.Fatalf("в лампочке должен быть код ошибки, там %q", note)
	}
}

// Пока первая проверка идёт, красный цвет врёт: ничего ещё не сломалось.
// Именно это видел тестер сразу после запуска.
func TestCheckNotDoneYetIsNotAFailure(t *testing.T) {
	srv, _ := newTestServer(t, withClientID, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	// Вход сохранён, но CheckAccount ещё ни разу не отработал.
	srv.syncSpotifyInfo()

	for _, c := range srv.state.Snapshot().Connections {
		if c.Name != "Spotify" {
			continue
		}
		if c.Level == app.ConnFail {
			t.Fatalf("до первой проверки лампочка не должна быть красной: %q", c.Detail)
		}
		if !strings.Contains(c.Detail, "Проверяю") {
			t.Fatalf("должно быть видно, что проверка идёт, а написано %q", c.Detail)
		}
	}
}

func connNote(t *testing.T, srv *Server, name string) string {
	t.Helper()
	for _, c := range srv.state.Snapshot().Connections {
		if c.Name == name {
			return c.Detail
		}
	}
	t.Fatalf("лампочки %q нет", name)
	return ""
}

// withClientID — Client ID вписан. Без него лампочка честно говорит
// «Не настроено» и до проверки связи дело не доходит вовсе.
func withClientID(c *config.Config) {
	c.SpotifyClientID = "проверочный"
	c.TwitchClientID = "проверочный"
}
