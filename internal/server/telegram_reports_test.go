package server

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"songrequest/internal/config"
	tg "songrequest/internal/telegram"
)

func TestTelegramHistoryPaginationAndFullCSV(t *testing.T) {
	s := commandServer(t)
	s.telegram.Period = time.Now().Add(-time.Hour).Unix()
	for i := 0; i < 23; i++ {
		_, err := s.db.SQL().Exec(`INSERT INTO history(requester,raw_request,provider,track_id,title,artist,outcome,reason,played_at) VALUES(?,'','spotify','',?,'Artist','played','',?)`, "Viewer", "=formula;\nTitle", time.Now().Unix())
		if err != nil {
			t.Fatal(err)
		}
	}
	for page, wantMore := range []bool{true, true, false} {
		text, more, err := s.telegramHistoryPage(context.Background(), page)
		if err != nil || more != wantMore || !strings.Contains(text, "Viewer") {
			t.Fatalf("page %d: %q %v %v", page, text, more, err)
		}
	}
	data, err := s.telegramHistoryCSV(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(data), "\xef\xbb\xbf")))
	r.Comma = ';'
	rows, err := r.ReadAll()
	if err != nil || len(rows) != 24 {
		t.Fatalf("CSV truncated: %d rows, %v", len(rows), err)
	}
	if rows[1][3] != "'=formula;\nTitle" {
		t.Fatal("spreadsheet formula or multiline field not protected", rows[1])
	}
}

func TestAutoRecapRetriesChannelWithoutRepeatingOwnerReport(t *testing.T) {
	s := commandServer(t)
	owner, photos := 0, 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "sendMessage") {
			owner++
		} else if strings.HasSuffix(r.URL.Path, "sendPhoto") {
			photos++
			if photos == 1 {
				w.WriteHeader(403)
				_, _ = w.Write([]byte(`{"ok":false,"error_code":403}`))
				return
			}
		} else {
			t.Errorf("unexpected API: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer api.Close()
	c := tg.New("fake")
	c.BaseURL = api.URL
	cfg := config.Telegram{OwnerID: 42, PublishChannel: "@channel"}
	if err := s.db.SetKV("telegram_live_start", "123"); err != nil {
		t.Fatal(err)
	}
	s.deliverTelegramRecap(context.Background(), c, cfg, "123")
	if pending, _, _ := s.db.GetKV("telegram_live_start"); pending != "123" {
		t.Fatal("failed channel delivery was forgotten")
	}
	s.deliverTelegramRecap(context.Background(), c, cfg, "123")
	if owner != 1 || photos != 2 {
		t.Fatalf("owner=%d photo attempts=%d", owner, photos)
	}
	if pending, _, _ := s.db.GetKV("telegram_live_start"); pending != "" {
		t.Fatal("completed delivery still pending")
	}
}

func TestOldReportButtonsRemainReadableButOldSettingsCannotChange(t *testing.T) {
	s := commandServer(t)
	reports := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "sendMessage") {
			reports++
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true})
	}))
	defer api.Close()
	c := tg.New("fake")
	c.BaseURL = api.URL
	cfg := config.Telegram{OwnerID: 42}
	for _, action := range []string{"stats", "setting:auto"} {
		s.telegramUpdate(context.Background(), c, cfg, tg.Update{Callback: &tg.Callback{ID: "test", From: tg.User{ID: 42}, Message: tg.Message{Date: 100, Chat: tg.Chat{ID: 42, Type: "private"}}, Data: action}}, 200)
	}
	if reports != 1 || s.cfg.Get().Telegram.AutoRecap {
		t.Fatal("old controls changed preferences or old reports stopped working")
	}
}

func TestOnlyOwnerCanChangeTelegramPreferences(t *testing.T) {
	s := commandServer(t)
	cfg := config.Telegram{Enabled: true, OwnerID: 42, PublishChannel: "@channel"}
	if err := s.cfg.Update(func(c *config.Config) { c.Telegram = cfg }); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"ok":true,"result":true}`)) }))
	defer api.Close()
	c := tg.New("fake")
	c.BaseURL = api.URL
	for _, sender := range []int64{7, 42} {
		s.telegramUpdate(context.Background(), c, cfg, tg.Update{Callback: &tg.Callback{ID: "test", From: tg.User{ID: sender}, Message: tg.Message{Date: 200, Chat: tg.Chat{ID: 42, Type: "private"}}, Data: "setting:auto"}}, 100)
		updated := s.cfg.Get().Telegram
		if updated.AutoRecap != (sender == 42) || updated.OwnerID != 42 || updated.PublishChannel != "@channel" || !updated.Enabled {
			t.Fatal("preferences changed outside owner permission", updated)
		}
	}
}
