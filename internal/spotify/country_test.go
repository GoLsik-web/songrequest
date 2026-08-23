package spotify

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"songrequest/internal/errs"
)

// Из лога тестера: при отвалившемся VPN Spotify отвечает 403 «недоступен в
// этой стране», а приложение говорило про отсутствие Premium. Это неправда и
// уводит человека чинить не то.
func TestCountryBlockIsNotReportedAsMissingPremium(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{ "error" : { "status" : 403, "message" : "Spotify is unavailable in this country" } }`))
	})

	_, err := c.CheckAccount(context.Background())

	if errs.CodeOf(err) == errs.SpotifyNoPremium {
		t.Fatal("к подписке это отношения не имеет")
	}
	if errs.CodeOf(err) != errs.SpotifyCountry {
		t.Fatalf("ждали код %s, получили %v", errs.SpotifyCountry, err)
	}

	var e *errs.Error
	errs.As(err, &e)
	if !strings.Contains(e.Message, "VPN") {
		t.Fatalf("человеку надо подсказать, что делать: %q", e.Message)
	}
}
