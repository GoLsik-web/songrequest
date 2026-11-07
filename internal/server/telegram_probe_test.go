package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"songrequest/internal/config"
	"strings"
	"testing"

	tg "songrequest/internal/telegram"
)

func TestTelegramProbeDoesNotDeleteWebhookOrSendToOtherUsers(t *testing.T) {
	for _, hook := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "webhook"}[hook], func(t *testing.T) {
			sends := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				result := any(true)
				switch {
				case strings.HasSuffix(r.URL.Path, "getMe"):
					result = map[string]string{"username": "my_bot"}
				case strings.HasSuffix(r.URL.Path, "getWebhookInfo"):
					url := ""
					if hook {
						url = "https://example.invalid/webhook"
					}
					result = map[string]string{"url": url}
				case strings.HasSuffix(r.URL.Path, "sendMessage"):
					var payload struct {
						Chat int64 `json:"chat_id"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Chat != 42 {
						t.Error("wrong recipient", err)
					}
					sends++
				default:
					t.Errorf("unexpected mutation/API: %s", r.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
			}))
			defer api.Close()
			c := tg.New("fake")
			c.BaseURL = api.URL
			username, err := telegramTestMessage(context.Background(), c, 42)
			if hook {
				if !errors.Is(err, errTelegramWebhook) || sends != 0 {
					t.Fatal("webhook was overridden", err, sends)
				}
			} else if err != nil || username != "my_bot" || sends != 1 {
				t.Fatal(username, err, sends)
			}
		})
	}
}

func TestSupportBotCannotBeUsedAsPersonalCompanion(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "getMe") {
			t.Errorf("support bot must not be polled or messaged: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"username":"songrequest_wrapped_bot"}}`))
	}))
	defer api.Close()
	c := tg.New("fake")
	c.BaseURL = api.URL
	if _, err := telegramTestMessage(context.Background(), c, 42); !errors.Is(err, errTelegramSupportBot) {
		t.Fatal("reserved bot accepted", err)
	}
	s := commandServer(t)
	s.runTelegram(context.Background(), c, config.Telegram{Enabled: true, OwnerID: 42})
	status := s.telegramStatus()
	if status["connected"] != false || status["note"] != errTelegramSupportBot.Error() {
		t.Fatal("missing explanation", status)
	}
}
