package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRelayUsesPrivateAuthorizationHeaderAndNeverFollowsRedirects(t *testing.T) {
	key := "private-app-link"
	redirected := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer other.Close()
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/telegram/getMe" || strings.Contains(r.URL.String(), key) || r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("invalid relay authorization")
		}
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer relay.Close()
	err := NewRelay(relay.URL, key).Call(context.Background(), "getMe", map[string]any{}, nil)
	if err == nil || strings.Contains(err.Error(), key) || redirected {
		t.Fatal("redirect/key exposure", err, redirected)
	}
}
