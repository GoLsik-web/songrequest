package spotify

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"songrequest/internal/errs"
)

// Найдено живьём на машине владельца: Spotify ограничил приложение и попросил
// паузу в четыре с половиной часа. Приложение паузу не отсиживало (правильно),
// но и не запоминало — а открытая панель спрашивает плеер раз в секунду. В
// логе за минуту набегала сотня одинаковых ошибок, и ограничение от такого
// стука только продлевается.
func TestLongPauseStopsFurtherRequests(t *testing.T) {
	c, calls, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "16000")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	ctx := context.Background()
	var out map[string]any

	if err := c.do(ctx, http.MethodGet, "/me/player", nil, &out); err == nil {
		t.Fatal("отказа не было, хотя Spotify ответил «слишком часто»")
	} else if errs.CodeOf(err) != errs.SpotifyRateLimit {
		t.Fatalf("код %s, а ждали SP-08", errs.CodeOf(err))
	}
	if len(*calls) != 1 {
		t.Fatalf("сходили %d раз, а ждали один", len(*calls))
	}

	err := c.do(ctx, http.MethodGet, "/me/player", nil, &out)
	if err == nil {
		t.Fatal("второй запрос прошёл, хотя пауза не кончилась")
	}
	if len(*calls) != 1 {
		t.Fatalf("во время паузы сходили в Spotify ещё %d раз", len(*calls)-1)
	}
	if _, text := errs.Describe(err); !strings.Contains(text, "осталось") {
		t.Errorf("человеку не сказали, сколько ждать: %q", text)
	}
}

// Пауза объявлена одному маршруту, а не приложению навсегда.
//
// Живьём вышло так: приложение сходило к Spotify напрямую из России, получило
// «подождите четыре часа», запомнило — и через двадцать секунд поднялся обход
// блокировок. Маршрут стал другой, а пауза продолжала действовать: заказы не
// работали весь вечер при полностью рабочем обходе.
func TestNewRouteClearsPause(t *testing.T) {
	c, calls, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "16000")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	ctx := context.Background()
	var out map[string]any
	c.do(ctx, http.MethodGet, "/me/player", nil, &out)
	if c.PauseLeft() == 0 {
		t.Fatal("пауза не запомнилась")
	}

	// Сменили маршрут — пауза прежнего маршрута больше не действует.
	if err := c.SetTunnel("socks5://127.0.0.1:1", "обход внутри приложения"); err != nil {
		t.Fatal(err)
	}
	if c.PauseLeft() != 0 {
		t.Fatal("после смены маршрута пауза осталась")
	}

	// Возвращаем прямой путь, чтобы запрос дошёл до тестового сервера.
	c.SetProxy("")
	c.apiBase = srv.URL

	was := len(*calls)
	c.do(ctx, http.MethodGet, "/me/player", nil, &out)
	if len(*calls) == was {
		t.Fatal("после смены маршрута приложение так и не попробовало сходить")
	}
	if !c.RecentlyLimited() {
		t.Error("жалоба Spotify забыта — опрос не станет реже")
	}
}

// Кнопка «Проверить связь» обязана работать и во время паузы: человек её
// видит, и другого способа проверить у него нет.
func TestClearPauseLetsPersonTryAgain(t *testing.T) {
	c, calls, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "16000")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	ctx := context.Background()
	var out map[string]any
	c.do(ctx, http.MethodGet, "/me/player", nil, &out)

	c.ClearPause()
	was := len(*calls)
	c.do(ctx, http.MethodGet, "/me/player", nil, &out)
	if len(*calls) == was {
		t.Fatal("после ClearPause запрос всё равно не ушёл")
	}
}

// Пауза держит ту часть Spotify, на которую он пожаловался, — и только её.
//
// Живьём 30.08: Spotify закрыл плеер на четыре часа, а поиск в это же время
// работал прекрасно. Приложение же гасило всё сразу, и панель показывала
// «ничего не работает» там, где не работала одна часть.
//
// Внутри одной части пауза общая: у приложения три источника запросов (опрос
// своей музыки, проверка играющего заказа, подбор трека), и стучаться втроём
// в закрытую дверь — верный способ выпросить вместо двадцати секунд четыре
// часа.
func TestPauseHoldsOnlyItsOwnPartOfSpotify(t *testing.T) {
	c, calls, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/me/player") {
			w.Header().Set("Retry-After", "20")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{}`))
	})

	// Первый запрос уходит в свой сон и вернётся нескоро — нам важно только,
	// что пауза для плеера уже объявлена.
	go c.do(context.Background(), http.MethodGet, "/me/player", nil, &map[string]any{})

	deadline := time.Now().Add(2 * time.Second)
	for c.PauseLeft() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if c.PauseLeft() == 0 {
		t.Fatal("пауза не объявлена остальным запросам")
	}

	was := len(*calls)
	err := c.do(context.Background(), http.MethodGet, "/me/player/devices", nil, &map[string]any{})
	if err == nil {
		t.Fatal("второй запрос к плееру ушёл в Spotify во время паузы")
	}
	if len(*calls) != was {
		t.Fatal("во время паузы приложение всё-таки постучалось к плееру")
	}
	if _, text := errs.Describe(err); !strings.Contains(text, "короткую паузу") {
		t.Errorf("про короткую паузу человеку не сказали: %q", text)
	}

	// А поиск в это время обязан работать.
	if err := c.do(context.Background(), http.MethodGet, "/search", nil, &map[string]any{}); err != nil {
		t.Fatalf("поиск закрыли из-за паузы плеера: %v", err)
	}
}
