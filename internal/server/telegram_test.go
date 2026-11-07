package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"songrequest/internal/config"
	tg "songrequest/internal/telegram"
	"songrequest/internal/twitch"
)

func TestTelegramPrivateOwnerOnlyAndStaleControls(t *testing.T) {
	s := commandServer(t)
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls++
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer api.Close()
	c := tg.New("test")
	c.BaseURL = api.URL
	cfg := config.Telegram{OwnerID: 42}
	now := time.Now().Unix()
	for _, test := range []struct {
		from, chat int64
		kind       string
	}{{7, 42, "private"}, {42, -10042, "supergroup"}, {7, 7, "private"}} {
		s.telegramUpdate(context.Background(), c, cfg, tg.Update{Message: &tg.Message{From: tg.User{ID: test.from}, Chat: tg.Chat{ID: test.chat, Type: test.kind}, Text: "/close", Date: now}}, now)
	}
	if calls != 0 || s.state.Snapshot().Paused {
		t.Fatal("unauthorized user reached controls")
	}
	s.telegramUpdate(context.Background(), c, cfg, tg.Update{Message: &tg.Message{From: tg.User{ID: 42}, Chat: tg.Chat{ID: 42, Type: "private"}, Text: "/close", Date: now - 60}}, now)
	if s.state.Snapshot().Paused {
		t.Fatal("old message executed")
	}
	s.telegramUpdate(context.Background(), c, cfg, tg.Update{Callback: &tg.Callback{From: tg.User{ID: 42}, Message: tg.Message{Chat: tg.Chat{ID: 42, Type: "private"}, Date: now - 60}, Data: "close"}}, now)
	if s.state.Snapshot().Paused {
		t.Fatal("old panel executed")
	}
	s.telegramUpdate(context.Background(), c, cfg, tg.Update{Message: &tg.Message{From: tg.User{ID: 42}, Chat: tg.Chat{ID: 42, Type: "private"}, Text: "/stats", Date: now}}, now)
	if calls < 2 {
		t.Fatal("owner did not receive report")
	}
}

func TestTelegramPollingOffsetDeduplicatesAndCancels(t *testing.T) {
	s := commandServer(t)
	var mu sync.Mutex
	gets, sends := 0, 0
	waiting := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"username":"review_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/getWebhookInfo"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"url":""}}`))
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			mu.Lock()
			gets++
			n := gets
			mu.Unlock()
			if n == 3 {
				close(waiting)
				<-r.Context().Done()
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []tg.Update{{ID: 17, Message: &tg.Message{From: tg.User{ID: 42}, Chat: tg.Chat{ID: 42, Type: "private"}, Text: "/stats", Date: time.Now().Unix()}}}})
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			mu.Lock()
			sends++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		default:
			t.Errorf("unexpected API: %s", r.URL.Path)
		}
	}))
	defer api.Close()
	c := tg.New("test")
	c.BaseURL = api.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop, done := s.startTelegram(ctx, c, config.Telegram{Enabled: true, OwnerID: 42})
	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("polling never started")
	}
	stop()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("polling ignored cancellation")
	}
	mu.Lock()
	defer mu.Unlock()
	if sends != 1 {
		t.Fatalf("duplicate update sent %d reports", sends)
	}
	offset, _, err := s.db.GetKV("telegram_offset:review_bot")
	if err != nil || offset != "18" {
		t.Fatalf("offset not persisted: %q %v", offset, err)
	}
}
func TestTelegramSecretsAndValidation(t *testing.T) {
	s := commandServer(t)
	s.secrets = &fakeSecrets{}
	token := "123456:" + strings.Repeat("a", 30)
	put := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.handleTelegramSet(w, httptest.NewRequest("POST", "/api/telegram", strings.NewReader(body)))
		return w
	}
	w := put(`{"enabled":true,"owner_id":42,"token":"` + token + `"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.handleTelegramGet(w, httptest.NewRequest("GET", "/api/telegram", nil))
	raw, _ := json.Marshal(s.cfg.Get())
	if strings.Contains(w.Body.String(), token) || strings.Contains(string(raw), token) {
		t.Fatal("token leaked into config or API")
	}
	old := s.cfg.Get().Telegram
	for _, body := range []string{`{"enabled":true,"owner_id":-1}`, `{"owner_id":42,"publish_channel":"https://bad.example"}`, `{"token":"invalid"}`} {
		if put(body).Code < 400 {
			t.Fatal("invalid settings accepted")
		}
		if s.cfg.Get().Telegram != old {
			t.Fatal("invalid settings replaced config")
		}
	}
	if put(`{"enabled":false,"forget":true}`).Code != 200 {
		t.Fatal("cannot forget token")
	}
	if s.telegramStatus()["has_token"] != false {
		t.Fatal("token was not deleted")
	}
}
func TestTelegramRecapIncludesMoreThan200OrdersAndKeepsChatAcrossRestart(t *testing.T) {
	s := commandServer(t)
	s.telegram.Period = time.Now().Add(-time.Hour).Unix()
	for i := 0; i < 230; i++ {
		_, err := s.db.SQL().Exec(`INSERT INTO history(requester,raw_request,provider,track_id,title,artist,outcome,reason,played_at) VALUES('Viewer','','spotify','','Song','Artist','played','',?)`, time.Now().Unix())
		if err != nil {
			t.Fatal(err)
		}
	}
	if text := s.telegramRecap(context.Background(), false); !strings.Contains(text, "230") {
		t.Fatal("report truncated", text)
	}
	_ = s.cfg.Update(func(c *config.Config) { c.Telegram.Enabled = true })
	s.telegramChat(twitch.ChatMessage{Login: "viewer", DisplayName: "Viewer", Text: "HELLO WORLD"})
	s.saveTelegramChat()
	s.telegram.mu.Lock()
	s.telegram.Period = 0
	s.telegram.Chats = nil
	s.telegram.mu.Unlock()
	s.loadTelegramChat()
	if !strings.Contains(s.telegramChatText(), "Viewer") {
		t.Fatal("chat lost on restart")
	}
}
