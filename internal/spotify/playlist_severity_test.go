package spotify

import (
	"context"
	"net/http"
	"songrequest/internal/errs"
	"testing"
)

func TestPlaylistAccessRestrictionIsNotMissingPremium(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"status":403,"message":"Forbidden"}}`))
	})
	err := c.do(context.Background(), http.MethodGet, "/playlists/test/tracks?limit=10", nil, nil)
	if errs.CodeOf(err) != errs.SpotifyPlaylistRestricted || errs.Severity(err) != "warn" {
		t.Fatalf("playlist restriction misclassified: %v", err)
	}
}
