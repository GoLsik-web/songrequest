package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSharedTelegramPairingNeverExposesDeviceKeyAndUsesConfirmedOwner(t *testing.T) {
	key := strings.Repeat("private-device-key-", 3)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reply any
		switch r.URL.Path {
		case "/pair/start":
			reply = map[string]any{"key": key, "link": "https://t.me/songrequest_streamer_bot?start=link_test"}
		case "/pair/status":
			reply = map[string]any{"key": key, "state": "confirm", "owner_id": 42, "name": "owner"}
		case "/pair/confirm":
			reply = map[string]any{"state": "connected", "owner_id": 42}
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
		if r.URL.Path != "/pair/start" && r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("missing device authorization")
		}
		_ = json.NewEncoder(w).Encode(reply)
	}))
	defer relay.Close()
	s := commandServer(t)
	s.secrets = &fakeSecrets{}
	start := httptest.NewRecorder()
	s.handleTelegramPairStart(start, httptest.NewRequest("POST", "/api/telegram/pair", strings.NewReader(`{"url":"`+relay.URL+`"}`)))
	if start.Code != 200 || strings.Contains(start.Body.String(), key) {
		t.Fatal("pairing failed or key leaked", start.Code, start.Body.String())
	}
	status := httptest.NewRecorder()
	s.handleTelegramPairStatus(status, httptest.NewRequest("POST", "/api/telegram/pair/status", nil))
	if status.Code != 200 || strings.Contains(status.Body.String(), key) {
		t.Fatal("device key exposed to page")
	}
	confirmed := httptest.NewRecorder()
	s.handleTelegramPairConfirm(confirmed, httptest.NewRequest("POST", "/api/telegram/pair/confirm", nil))
	if confirmed.Code != 200 || !s.cfg.Get().Telegram.Enabled || s.cfg.Get().Telegram.OwnerID != 42 {
		t.Fatal("binding failed", confirmed.Body.String())
	}
	saved := httptest.NewRecorder()
	s.handleTelegramSet(saved, httptest.NewRequest("POST", "/api/telegram", strings.NewReader(`{"mode":"shared","enabled":true,"owner_id":999}`)))
	if saved.Code != 200 || s.cfg.Get().Telegram.OwnerID != 42 {
		t.Fatal("page could override confirmed owner")
	}
	configJSON, _ := json.Marshal(s.cfg.Get())
	if strings.Contains(string(configJSON), key) {
		t.Fatal("device key leaked into config")
	}
}

func TestSharedTelegramRejectsUnsafeRelayURLs(t *testing.T) {
	for _, url := range []string{"http://example.com", "https://name:password@example.com", "https://example.com?key=secret", "https://example.com/other", "file:///tmp/key"} {
		if validRelayURL(url) {
			t.Errorf("unsafe URL accepted: %s", url)
		}
	}
	for _, url := range []string{"http://127.0.0.1:8980", "https://bot.example.com"} {
		if !validRelayURL(url) {
			t.Errorf("valid URL rejected: %s", url)
		}
	}
}
