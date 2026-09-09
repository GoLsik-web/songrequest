package spotify

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"songrequest/internal/errs"
)

// Сколько результатов просить у поиска.
//
// История вопроса. 27.08 пять дней не работал поиск текстом: Spotify у тестера
// отвечал «400 Invalid limit» на 20 и 50, а приложение просило ровно столько.
// Тогда появился запасной путь — переспросить меньшим числом и запомнить
// потолок.
//
// В феврале 2026 Spotify опустил предел поиска до десяти для всех, а значение
// по умолчанию — до пяти. То, что было особенностью одного аккаунта, стало
// общим правилом. Поэтому приложение больше не просит больше десяти вовсе:
// иначе каждый запуск начинался бы с заведомо пустого запроса и красной строки
// в логе.
//
// Запасной путь при этом остался: если Spotify однажды урежет предел ещё
// сильнее, приложение переживёт это так же, как пережило прошлый раз.

// Больше десяти не просим никогда, сколько бы ни попросил вызывающий код.
func TestSearchNeverAsksAboveTheCap(t *testing.T) {
	c, calls, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tracks":{"items":[]}}`))
	})

	for _, want := range []int{20, 50, 100} {
		if _, err := c.SearchTracks(context.Background(), "into you", want); err != nil {
			t.Fatal(err)
		}
	}
	if len(*calls) != 3 {
		t.Fatalf("лишние запросы: %d", len(*calls))
	}
	for i, call := range *calls {
		if !strings.Contains(call.Query, "limit=10") {
			t.Fatalf("запрос %d ушёл не с limit=10: %s", i+1, call.Query)
		}
	}
}

// А если Spotify урежет предел ещё сильнее, заказ не должен пропадать: отказ
// на большой limit заставляет переспросить меньшим числом.
func TestBigLimitFallsBackToSmall(t *testing.T) {
	// Приложение просит десять; представим, что этот Spotify согласен только
	// на пять. Занижаем порог прямо в клиенте — так же, как это сделал бы
	// живой отказ на прошлом поиске.
	c, calls, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "limit=10") {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error": {"status": 400, "message": "Invalid limit" } }`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tracks":{"items":[
			{"id":"t1","uri":"spotify:track:t1","name":"Into You","duration_ms":244000,
			 "artists":[{"name":"Ariana Grande"}],"album":{"name":"Dangerous Woman"}}
		]}}`))
	})
	// Пять — то, на что этот выдуманный Spotify согласен.
	c.capSearchLimit(5)

	got, err := c.SearchTracks(context.Background(), "into you", 50)
	if err != nil {
		t.Fatalf("поиск обязан пережить отказ на большой limit: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Into You" {
		t.Fatalf("трек потерялся: %+v", got)
	}
	if len(*calls) != 1 {
		t.Fatalf("ждали один запрос уже с заниженным числом, вышло %d: %+v", len(*calls), *calls)
	}
	if !strings.Contains((*calls)[0].Query, "limit=5") {
		t.Fatalf("запрос ушёл не с limit=5: %s", (*calls)[0].Query)
	}
}

// Потолок выясняется один раз за запуск: платить лишним запросом за каждый
// поиск до конца стрима — это удвоенная нагрузка и лишняя секунда на заказ.
func TestLimitCapIsRememberedForTheSession(t *testing.T) {
	c, calls, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "limit=10") {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error": {"status": 400, "message": "Invalid limit" } }`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tracks":{"items":[]}}`))
	})

	if _, err := c.SearchTracks(context.Background(), "первый", 20); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SearchTracks(context.Background(), "второй", 20); err != nil {
		t.Fatal(err)
	}

	// Первый поиск: отказ на десять и повтор меньшим. Второй: сразу меньшим.
	if len(*calls) != 3 {
		t.Fatalf("ждали три запроса, вышло %d: %+v", len(*calls), *calls)
	}
	if strings.Contains((*calls)[2].Query, "limit=10") {
		t.Fatalf("второй поиск обязан сразу идти малым числом: %s", (*calls)[2].Query)
	}
}

// «Кончился дневной запас» и «слишком часто» — разные беды.
//
// С июля 2026 Spotify кладёт в тело отказа поле reason. Раньше приложение
// видело только 429 и говорило человеку одно и то же, а пауза при этом бывала
// то двадцать секунд, то двадцать один час — и понять, что происходит, было
// нельзя.
func TestQuotaExceededIsToldApartFromRateLimit(t *testing.T) {
	bodies := map[string]bool{
		`{"reason":"QUOTA_EXCEEDED"}`:                         true,
		`{"error":{"status":429,"reason":"QUOTA_EXCEEDED"}}`:  true,
		`{"reason":"quota_exceeded"}`:                         true,
		`{"error":{"status":429,"message":"API rate limit"}}`: false,
		`{}`:            false,
		``:              false,
		`не json вовсе`: false,
	}
	for body, want := range bodies {
		if got := isQuotaExceeded([]byte(body)); got != want {
			t.Errorf("isQuotaExceeded(%q) = %v, ждали %v", body, got, want)
		}
	}
}

// Долгий отказ с исчерпанным запасом должен приезжать своим кодом: по нему
// панель и лог отличают «ждать сутки» от «сбавить темп».
func TestQuotaExhaustionHasOwnCode(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "77000")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"reason":"QUOTA_EXCEEDED"}`))
	})

	_, err := c.SearchTracks(context.Background(), "что угодно", 10)
	if err == nil {
		t.Fatal("отказ не пришёл")
	}
	if code := errs.CodeOf(err); code != errs.SpotifyQuota {
		t.Fatalf("код отказа %q, ждали %q", code, errs.SpotifyQuota)
	}
}
