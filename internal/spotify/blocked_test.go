package spotify

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"songrequest/internal/errs"
)

// Четвёртый день подряд Spotify отвечает тестеру «400 Invalid limit» на
// каждый поиск трека, а панель показывала «Spotify отказал (400). Подробности
// в логе» — сообщение, по которому человек не сделает ничего. Сказать надо
// то, что от него нужно: прислать лог.
func TestBogusInvalidLimitAsksForLog(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": {"status": 400, "message": "Invalid limit" } }`))
	})

	_, err := c.SearchTracks(context.Background(), "три дня дождя прощание", 20)
	if err == nil {
		t.Fatal("отказ обязан дойти до вызывающего")
	}
	code, text := errs.Describe(err)
	if code != errs.SpotifySearchLimit {
		t.Fatalf("у этого отказа обязан быть свой код, а не %q", code)
	}
	if !strings.Contains(text, "лог") {
		t.Fatalf("человека надо попросить прислать лог, а не %q", text)
	}
	if strings.Contains(text, "Подробности в логе") {
		t.Fatalf("это и есть то сообщение, от которого уходили: %q", text)
	}
}

// Обратная сторона: обычный отказ Spotify не должен выдаваться за эту
// историю. Такой запрос приложение не шлёт, но проверка дешёвая.
func TestRealLimitErrorStaysItself(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": {"status": 400, "message": "invalid id" } }`))
	})

	_, err := c.SearchTracks(context.Background(), "что угодно", 20)
	if err == nil {
		t.Fatal("отказ обязан дойти до вызывающего")
	}
	if code, _ := errs.Describe(err); code == errs.SpotifySearchLimit {
		t.Fatal("обычный 400 не должен выдаваться за историю с limit")
	}
}
