package spotify

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Пять дней у тестера не работал поиск текстом. Пробы 27.08 показали причину:
// его Spotify отвечает 200 на limit=1, 5 и 10 и «400 Invalid limit» на 20 и
// 50 — а приложение просило ровно 20 и 50. Значит поиск не находил ничего
// никогда, и вместе с ним не работали ссылки на YouTube и Яндекс.Музыку:
// их название уходит в тот же поиск.
//
// Проверяем: отказ на большой limit не отменяет заказ, а заставляет
// переспросить меньшим числом.
func TestBigLimitFallsBackToSmall(t *testing.T) {
	c, calls, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "limit=50") ||
			strings.Contains(r.URL.RawQuery, "limit=20") {
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

	got, err := c.SearchTracks(context.Background(), "into you", 50)
	if err != nil {
		t.Fatalf("поиск обязан пережить отказ на большой limit: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Into You" {
		t.Fatalf("трек потерялся: %+v", got)
	}
	if len(*calls) != 2 {
		t.Fatalf("ждали два запроса — большой и повтор меньшим, вышло %d", len(*calls))
	}
	if !strings.Contains((*calls)[1].Query, "limit=10") {
		t.Fatalf("повтор обязан идти с limit=10: %s", (*calls)[1].Query)
	}
}

// Потолок выясняется один раз за запуск: платить лишним запросом за каждый
// поиск до конца стрима — это удвоенная нагрузка и лишняя секунда на заказ.
func TestLimitCapIsRememberedForTheSession(t *testing.T) {
	c, calls, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "limit=50") {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error": {"status": 400, "message": "Invalid limit" } }`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tracks":{"items":[]}}`))
	})

	if _, err := c.SearchTracks(context.Background(), "первый", 50); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SearchTracks(context.Background(), "второй", 50); err != nil {
		t.Fatal(err)
	}

	// Первый поиск: отказ + повтор. Второй: сразу малым числом.
	if len(*calls) != 3 {
		t.Fatalf("ждали три запроса, вышло %d: %+v", len(*calls), *calls)
	}
	if strings.Contains((*calls)[2].Query, "limit=50") {
		t.Fatalf("второй поиск обязан сразу идти малым числом: %s", (*calls)[2].Query)
	}
}

// Аккаунты без этого ограничения не должны страдать: пятьдесят просили не
// просто так — чем шире выбор, тем точнее подбор среди похожих названий.
func TestNormalAccountKeepsBigLimit(t *testing.T) {
	c, calls, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tracks":{"items":[]}}`))
	})

	if _, err := c.SearchTracks(context.Background(), "into you", 50); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("лишние запросы на здоровом аккаунте: %d", len(*calls))
	}
	if !strings.Contains((*calls)[0].Query, "limit=50") {
		t.Fatalf("занижать всем подряд нельзя: %s", (*calls)[0].Query)
	}
}
