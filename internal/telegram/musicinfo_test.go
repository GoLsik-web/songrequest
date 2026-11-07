package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMusicLookupMatchesConservatively(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("User-Agent"), "github.com/GoLsik-web/songrequest") {
			t.Error("missing contact")
		}
		if r.URL.Query().Get("fmt") != "json" {
			t.Error("missing format")
		}
		if r.URL.Path == "/artist" {
			_, _ = w.Write([]byte(`{"artists":[{"score":70,"tags":[{"name":"pop","count":10}]},{"score":"100","tags":[{"name":"alternative rock","count":5}]}]}`))
		} else {
			_, _ = w.Write([]byte(`{"recordings":[{"score":50,"first-release-date":"1920"},{"score":100,"first-release-date":"1998-01"},{"score":"100","first-release-date":"2004"}]}`))
		}
	}))
	defer srv.Close()
	info, err := MusicLookup(context.Background(), srv.Client(), srv.URL, "artist", `Quoted " artist`, "")
	if err != nil || info.Genre != "Рок" {
		t.Fatal(info, err)
	}
	info, err = MusicLookup(context.Background(), srv.Client(), srv.URL, "recording", "Artist", "Song")
	if err != nil || info.Year != 1998 {
		t.Fatal(info, err)
	}
}
