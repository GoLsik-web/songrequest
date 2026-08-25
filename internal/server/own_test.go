package server

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"songrequest/internal/app"
	"songrequest/internal/config"
	"songrequest/internal/player"
	"songrequest/internal/queue"
)

// Между заказами в кадре должно быть то, что стример слушает сам, — иначе
// виджет пустует бо́льшую часть стрима.
func TestOwnMusicShowsWhenQueueIsEmpty(t *testing.T) {
	srv, _ := newTestServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/me/player" && r.Method == http.MethodGet {
			writeTestJSON(w, map[string]any{
				"is_playing": true, "progress_ms": 42000,
				"item": map[string]any{
					"id": "own1", "uri": "spotify:track:own1", "name": "Пыль",
					"duration_ms": 200000,
					"artists":     []any{map[string]any{"name": "Сплин"}},
					"album": map[string]any{"name": "Гранатовый альбом",
						"images": []any{map[string]any{"url": "https://cover"}}},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	srv.pollOwn(context.Background())

	now := srv.state.Snapshot().Now
	if now == nil {
		t.Fatal("своя музыка не попала в состояние — виджет останется пустым")
	}
	if now.Title != "Пыль" || now.Artist != "Сплин" {
		t.Fatalf("взялось не то: %s — %s", now.Artist, now.Title)
	}
	if now.Source != app.SourceOwn {
		t.Fatalf("трек должен быть помечен как своя музыка, помечен %q", now.Source)
	}
}

// Заказ важнее: пока он играет, спрашивать Spotify не о чем, и своя музыка
// не должна перебивать его в кадре.
func TestOrderBeatsOwnMusic(t *testing.T) {
	// Считаем обращения, а не факт обращения: плеер и сам разок спрашивает
	// Spotify, что играет, — и флаг «спрашивали» ловил бы именно его.
	var mu sync.Mutex
	asks := 0
	srv, _ := newTestServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/me/player" && r.Method == http.MethodGet {
			mu.Lock()
			asks++
			mu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
	})
	countAsks := func() int {
		mu.Lock()
		defer mu.Unlock()
		return asks
	}

	// Плеера в тестовом сервере нет: он не нужен почти нигде, а здесь как раз
	// проверяется, кто кого перебивает, — поэтому поднимаем настоящий.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.player = player.New(srv.queue, srv.spotify, srv.log)
	srv.player.OnChange = srv.syncPlayback
	go srv.player.Run(ctx)

	// Своя музыка уже была в состоянии...
	srv.ownNow = &app.NowPlaying{Source: app.SourceOwn, Title: "Пыль", Artist: "Сплин"}

	// ...а теперь заиграл заказ.
	if _, err := srv.queue.Add(queue.Item{
		Source: queue.SourcePoints, Requester: "Viewer", Provider: "spotify",
		TrackID: "ordered", URI: "spotify:track:ordered",
		Title: "Bohemian Rhapsody", Artist: "Queen", DurationMs: 60000,
	}, false); err != nil {
		t.Fatal(err)
	}
	srv.player.Nudge()
	waitFor(t, "заказ так и не заиграл", func() bool { return srv.player.Now() != nil })

	before := countAsks()
	srv.pollOwn(ctx)

	if countAsks() != before {
		t.Error("пока играет заказ, спрашивать Spotify не о чем")
	}
	now := srv.state.Snapshot().Now
	if now == nil || now.Title != "Bohemian Rhapsody" {
		t.Fatalf("в кадре должен быть заказ, а там %+v", now)
	}
	if now.Source != app.SourceOrder {
		t.Fatalf("заказ должен быть помечен заказом, помечен %q", now.Source)
	}
}

// Свой плейлист светить хотят не все.
func TestOwnMusicHiddenWhenTurnedOff(t *testing.T) {
	var asked bool
	srv, _ := newTestServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/me/player") {
			asked = true
		}
		w.WriteHeader(http.StatusNoContent)
	})

	srv.cfg.Update(func(c *config.Config) { c.Widget.ShowOwn = false })
	srv.pollOwn(context.Background())

	if asked {
		t.Error("с выключенным показом своей музыки Spotify дёргать незачем")
	}
	if now := srv.state.Snapshot().Now; now != nil {
		t.Fatalf("в кадре не должно быть ничего, а там %+v", now)
	}
}

// waitFor ждёт, пока условие станет верным. Плеер работает в своей горутине,
// и проверять сразу после Nudge — значит ловить гонку через раз.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(what)
}
