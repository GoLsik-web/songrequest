package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"songrequest/internal/config"
)

// Сквозная проверка того, на что жаловался тестер: Spotify не прислал product,
// приложение обязано остаться рабочим и честно сказать, что не знает.
func TestPanelShowsUnknownPlanWithoutBlocking(t *testing.T) {
	srv, _ := newTestServer(t,
		func(c *config.Config) { c.SpotifyClientID = "есть" },
		func(w http.ResponseWriter, r *http.Request) {
			writeTestJSON(w, map[string]any{
				"id": "tester", "display_name": "Тестер", "email": "t@example.com",
			})
		})

	me, err := srv.spotify.CheckAccount(context.Background())
	if err != nil {
		t.Fatalf("это не сбой связи: %v", err)
	}
	if problem := srv.spotify.PlanProblem(me); problem != nil {
		srv.reportPlanProblem(problem)
	}
	srv.SyncSpotify()

	snap := srv.state.Snapshot()
	sp := snap.Spotify

	if sp.Plan != "unknown" {
		t.Fatalf("подписка должна быть «не определена», а не «нет»: %q", sp.Plan)
	}
	if sp.Email != "t@example.com" {
		t.Fatalf("в панели должна быть почта аккаунта: %q", sp.Email)
	}
	if sp.PlanNoteCode != "SP-15" {
		t.Fatalf("ждали код SP-15, получили %q", sp.PlanNoteCode)
	}

	// Лампочка горит: работать можно.
	for _, c := range snap.Connections {
		if c.Name != "Spotify" {
			continue
		}
		if !c.Connected {
			t.Fatalf("неизвестная подписка не повод гасить подключение: %q", c.Detail)
		}
		if !strings.Contains(c.Detail, "не определена") {
			t.Fatalf("в ленте должно быть видно, что подписка не определена: %q", c.Detail)
		}
	}

	// В хронике — предупреждение, а не ошибка.
	last := snap.Notices[len(snap.Notices)-1]
	if last.Level != "warn" {
		t.Fatalf("это предупреждение, а не ошибка: %+v", last)
	}
}
