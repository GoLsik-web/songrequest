package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIHonorsRetryAndNeverLeaksToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"secret-token","parameters":{"retry_after":12}}`))
	}))
	defer srv.Close()
	c := New("secret-token")
	c.BaseURL = srv.URL
	err := c.Call(context.Background(), "getUpdates", map[string]any{"offset": 42}, nil)
	var api *APIError
	if !errors.As(err, &api) || api.Code != 429 || api.RetryAfter != 12 {
		t.Fatalf("retry lost: %v", err)
	}
	if strings.Contains(err.Error(), c.Token) {
		t.Fatal("API error leaked token")
	}
	srv.Close()
	err = c.Call(context.Background(), "getMe", nil, nil)
	if err == nil || strings.Contains(err.Error(), c.Token) || strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("transport error leaked URL: %v", err)
	}
}
func TestAPIAndPhoto(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendPhoto") {
			if err := r.ParseMultipartForm(2 << 20); err != nil {
				t.Error(err)
			}
			if r.FormValue("chat_id") != "42" {
				t.Error("wrong photo destination")
			}
			f, _, err := r.FormFile("photo")
			if err != nil {
				t.Error(err)
			} else {
				defer f.Close()
				if _, err = png.Decode(f); err != nil {
					t.Error(err)
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]string{"username": "my_bot"}})
	}))
	defer srv.Close()
	c := New("fake")
	c.BaseURL = srv.URL
	var out struct {
		Username string `json:"username"`
	}
	if err := c.Call(context.Background(), "getMe", nil, &out); err != nil || out.Username != "my_bot" {
		t.Fatal(out, err)
	}
	pic, err := Card("Канал стримера", []string{"Итоги эфира", "Заказов: 123", strings.Repeat("длинное имя", 30)})
	if err != nil {
		t.Fatal(err)
	}
	im, err := png.Decode(bytes.NewReader(pic))
	if err != nil || im.Bounds().Dx() != 1080 || im.Bounds().Dy() != 1350 {
		t.Fatal("invalid recap dimensions", err)
	}
	if err = c.Photo(context.Background(), "42", "Итоги", pic); err != nil {
		t.Fatal(err)
	}
}
func TestValidToken(t *testing.T) {
	for _, v := range []string{"", "12345:abc", "12345:" + strings.Repeat("a", 20) + "/sendMessage", "abcde:" + strings.Repeat("a", 25)} {
		if ValidToken(v) {
			t.Errorf("accepted %q", v)
		}
	}
	if !ValidToken("123456:" + strings.Repeat("a", 25)) {
		t.Fatal("valid token rejected")
	}
}
