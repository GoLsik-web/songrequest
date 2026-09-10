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
	"songrequest/internal/smtc"
	"songrequest/internal/spotifyapp"
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

	// Программы Spotify на этом компьютере как будто нет: проверяем путь
	// «спросить по сети». На машине разработчика Spotify запущен, и без этой
	// подмены тест ловил бы настоящий трек вместо выдуманного.
	srv.localTrack = noLocalSpotify
	srv.panelTrack = noMediaPanel
	srv.pollOwn(context.Background(), &ownWatch{})

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
	srv.localTrack = noLocalSpotify
	srv.panelTrack = noMediaPanel
	srv.pollOwn(ctx, &ownWatch{})

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
	srv.localTrack = noLocalSpotify
	srv.panelTrack = noMediaPanel
	srv.pollOwn(context.Background(), &ownWatch{})

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

// noLocalSpotify изображает компьютер, на котором программа Spotify не
// запущена: тогда остаётся спрашивать по сети.
// noMediaPanel — системная панель Windows молчит.
//
// На машине разработчика Spotify запущен по-настоящему, и без этой подмены
// проверки ловили бы его живой трек вместо выдуманного. Проверки своей музыки
// написаны про прежний путь — чтение заголовка окна, — и должны его и
// проверять.
func noMediaPanel() (smtc.Now, bool, error) { return smtc.Now{}, false, nil }

func noLocalSpotify() (spotifyapp.Track, spotifyapp.Status) {
	return spotifyapp.Track{}, spotifyapp.Unknown
}

// Главное ради чего всё затевалось: пока Spotify не пускает приложение к
// плееру, играющий трек всё равно виден.
//
// Живьём 30.08: Spotify закрыл доступ к плееру на четыре часа, и панель
// показывала пустоту — при том, что программа Spotify рядом играла музыку и
// писала имя трека в заголовок своего окна.
func TestOwnMusicSurvivesSpotifyRefusal(t *testing.T) {
	srv, _ := newTestServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		// Spotify отказывает всему, что касается плеера.
		w.Header().Set("Retry-After", "16000")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	srv.localTrack = func() (spotifyapp.Track, spotifyapp.Status) {
		return spotifyapp.Track{Artist: "Hayd", Title: "Closure"}, spotifyapp.Playing
	}
	srv.panelTrack = noMediaPanel

	srv.pollOwn(context.Background(), &ownWatch{})

	now := srv.state.Snapshot().Now
	if now == nil {
		t.Fatal("трек не показан, хотя программа Spotify его играет")
	}
	if now.Artist != "Hayd" || now.Title != "Closure" {
		t.Fatalf("показано не то: %s — %s", now.Artist, now.Title)
	}
	if now.Source != app.SourceOwn {
		t.Fatalf("трек должен быть помечен как своя музыка, помечен %q", now.Source)
	}
}

// Пока играет один и тот же трек, по сети ходить незачем: имя трека приходит
// из заголовка окна бесплатно. Именно частота этих запросов и довела до
// четырёхчасовой паузы.
func TestSameTrackDoesNotAskSpotifyAgain(t *testing.T) {
	var mu sync.Mutex
	asks := 0
	srv, _ := newTestServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/me/player") {
			mu.Lock()
			asks++
			mu.Unlock()
			writeTestJSON(w, map[string]any{
				"is_playing": true, "progress_ms": 10000,
				"item": map[string]any{
					"id": "own1", "uri": "spotify:track:own1", "name": "Closure",
					"duration_ms": 200000,
					"artists":     []any{map[string]any{"name": "Hayd"}},
					"album":       map[string]any{"name": "Closure"},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv.localTrack = func() (spotifyapp.Track, spotifyapp.Status) {
		return spotifyapp.Track{Artist: "Hayd", Title: "Closure"}, spotifyapp.Playing
	}

	var w ownWatch
	srv.pollOwn(context.Background(), &w)
	srv.pollOwn(context.Background(), &w)
	srv.pollOwn(context.Background(), &w)

	mu.Lock()
	defer mu.Unlock()
	if asks != 1 {
		t.Fatalf("сходили к Spotify %d раза, а хватало одного", asks)
	}
}

// Положение внутри трека приложение считает само: между запросами музыка едет
// ровно так же, как едет время.
func TestPositionMovesWithoutAsking(t *testing.T) {
	w := ownWatch{
		now: &app.NowPlaying{Title: "Closure", Artist: "Hayd", DurationMs: 200000},
		pos: 10000,
		at:  time.Now().Add(-3 * time.Second),
	}

	got := w.playing(spotifyapp.Track{Artist: "Hayd", Title: "Closure"})
	if got.PositionMs < 12500 || got.PositionMs > 13500 {
		t.Fatalf("положение %d мс, а ждали около 13000", got.PositionMs)
	}

	// За конец трека уезжать нельзя: полоса в кадре уползла бы за край.
	w.at = time.Now().Add(-10 * time.Minute)
	if got := w.playing(spotifyapp.Track{}); got.PositionMs != 200000 {
		t.Fatalf("положение %d мс, а трек длится 200000", got.PositionMs)
	}
}

// Программа Spotify рядом и молчит — значит музыка стоит, и дёргать Spotify
// по сети каждую секунду незачем. Раз в минуту — на случай, если стример
// слушает с телефона.
func TestIdleSpotifyIsNotAskedOften(t *testing.T) {
	var mu sync.Mutex
	asks := 0
	srv, _ := newTestServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/me/player") {
			mu.Lock()
			asks++
			mu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv.localTrack = func() (spotifyapp.Track, spotifyapp.Status) {
		return spotifyapp.Track{}, spotifyapp.Idle
	}
	srv.panelTrack = noMediaPanel

	var w ownWatch
	for i := 0; i < 5; i++ {
		srv.pollOwn(context.Background(), &w)
	}

	mu.Lock()
	defer mu.Unlock()
	if asks != 1 {
		t.Fatalf("сходили к Spotify %d раз, а хватало одного", asks)
	}
}

// ── чтение из системной панели Windows ───────────────────────────────
//
// Ради неё всё и затевалось: Windows знает про играющую музыку всё, что нам
// нужно, и знает бесплатно. Проверяем, что приложение этим пользуется и не
// ходит в сеть там, где ходить не надо.

// panelPlaying — панель Windows показывает играющий трек.
func panelPlaying(status smtc.Status) func() (smtc.Now, bool, error) {
	return func() (smtc.Now, bool, error) {
		return smtc.Now{
			App: "SpotifyAB.SpotifyMusic_test!Spotify", Title: "Группа крови",
			Artist: "КИНО", Album: "Группа крови", Status: status,
			PositionMs: 42_000, DurationMs: 235_100,
		}, true, nil
	}
}

func TestPanelGivesTrackWithoutNetwork(t *testing.T) {
	asks := 0
	srv, _ := newTestServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		asks++
		w.WriteHeader(http.StatusNoContent)
	})
	srv.localTrack = noLocalSpotify
	srv.panelTrack = panelPlaying(smtc.StatusPlaying)
	// Обложка никому не нужна: панель закрыта, виджет её не показывает.
	srv.cfg.Update(func(c *config.Config) { c.Widget.ShowArt = false })

	srv.pollOwn(context.Background(), &ownWatch{})

	now := srv.state.Snapshot().Now
	if now == nil {
		t.Fatal("трек из панели Windows не попал в состояние")
	}
	if now.Artist != "КИНО" || now.Title != "Группа крови" {
		t.Fatalf("показано не то: %s — %s", now.Artist, now.Title)
	}
	// Главное: точное положение и длительность, которых у нас не было без сети.
	if now.PositionMs != 42_000 || now.DurationMs != 235_100 {
		t.Fatalf("время приехало не то: %d из %d", now.PositionMs, now.DurationMs)
	}
	if now.Source != app.SourceOwn {
		t.Fatalf("трек помечен как %q", now.Source)
	}
	if asks != 0 {
		t.Fatalf("сходили к Spotify %d раз, а панель Windows знает всё сама", asks)
	}
}

// Пауза своей музыки — то, чего приложение не видело в принципе: в заголовке
// окна Spotify её нет, а по сети за ней ходить слишком дорого.
func TestPanelSeesPause(t *testing.T) {
	srv, _ := newTestServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	srv.localTrack = noLocalSpotify
	srv.panelTrack = panelPlaying(smtc.StatusPaused)
	srv.cfg.Update(func(c *config.Config) { c.Widget.ShowArt = false })

	srv.pollOwn(context.Background(), &ownWatch{})

	now := srv.state.Snapshot().Now
	if now == nil {
		t.Fatal("трек не показан")
	}
	if !now.Paused {
		t.Fatal("пауза из панели Windows не доехала до состояния")
	}
}

// Остановлено — это тишина, а не «играет с нулевой позиции».
func TestPanelStoppedMeansSilence(t *testing.T) {
	srv, _ := newTestServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	srv.localTrack = noLocalSpotify
	srv.panelTrack = panelPlaying(smtc.StatusStopped)

	srv.pollOwn(context.Background(), &ownWatch{})

	if now := srv.state.Snapshot().Now; now != nil {
		t.Fatalf("при остановленном проигрывателе показан трек: %s", now.Title)
	}
}

// За обложкой ходим один раз на трек — даже если её так и не дали.
//
// Без этого приложение просило бы её на каждом опросе, то есть раз в две
// секунды: ровно та утечка, ради устранения которой всё и затевалось.
func TestPanelAsksCoverOncePerTrack(t *testing.T) {
	asks := 0
	srv, _ := newTestServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/me/player") {
			asks++
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv.localTrack = noLocalSpotify
	srv.panelTrack = panelPlaying(smtc.StatusPlaying)
	srv.cfg.Update(func(c *config.Config) { c.Widget.ShowArt = true })

	var w ownWatch
	for i := 0; i < 5; i++ {
		srv.pollOwn(context.Background(), &w)
	}
	if asks != 1 {
		t.Fatalf("за обложкой сходили %d раз, а хватало одного", asks)
	}
}

// Панель молчит — работают прежние пути, а не пустой экран.
func TestSilentPanelFallsBackToWindowTitle(t *testing.T) {
	srv, _ := newTestServer(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	srv.panelTrack = noMediaPanel
	srv.localTrack = func() (spotifyapp.Track, spotifyapp.Status) {
		return spotifyapp.Track{Artist: "Hayd", Title: "Closure"}, spotifyapp.Playing
	}

	srv.pollOwn(context.Background(), &ownWatch{})

	now := srv.state.Snapshot().Now
	if now == nil || now.Title != "Closure" {
		t.Fatalf("запасной путь не сработал: %+v", now)
	}
}
