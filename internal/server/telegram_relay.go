package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"songrequest/internal/config"
	tg "songrequest/internal/telegram"
)

// SharedRelayURL задаётся при сборке после настройки внешнего адреса ПК автора.
var SharedRelayURL = "http://127.0.0.1:8980"

const telegramRelayKey = "telegram_relay_link"

type telegramRelayLink struct {
	URL string `json:"url"`
	Key string `json:"key"`
}
type telegramRelayReply struct {
	Key      string `json:"key"`
	Link     string `json:"link"`
	State    string `json:"state"`
	OwnerID  int64  `json:"owner_id"`
	Name     string `json:"name"`
	Username string `json:"username"`
}

func validRelayURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")
}

func (s *Server) relayLink() (telegramRelayLink, error) {
	var link telegramRelayLink
	if s.secrets == nil {
		return link, fmt.Errorf("credential store unavailable")
	}
	if err := s.secrets.GetJSON(telegramRelayKey, &link); err != nil {
		return link, err
	}
	if !validRelayURL(link.URL) || len(link.Key) < 32 {
		return link, fmt.Errorf("invalid link")
	}
	return link, nil
}

func relayCall(ctx context.Context, link telegramRelayLink, path string, out any) error {
	if !validRelayURL(link.URL) {
		return fmt.Errorf("invalid relay address")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(link.URL, "/")+path, bytes.NewReader([]byte("{}")))
	if err != nil {
		return fmt.Errorf("invalid relay request")
	}
	req.Header.Set("Content-Type", "application/json")
	if link.Key != "" {
		req.Header.Set("Authorization", "Bearer "+link.Key)
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("общий бот недоступен; проверь, включён ли компьютер автора")
	}
	defer response.Body.Close()
	if path == "/pair/revoke" && response.StatusCode == http.StatusUnauthorized {
		return nil
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("общий бот ответил HTTP %d", response.StatusCode)
	}
	if out != nil && json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(out) != nil {
		return fmt.Errorf("не удалось прочитать ответ общего бота")
	}
	return nil
}

func (s *Server) handleTelegramPairStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in) != nil {
		http.Error(w, "Не разобрал адрес общего бота", 400)
		return
	}
	if in.URL == "" {
		in.URL = SharedRelayURL
	}
	if !validRelayURL(in.URL) {
		http.Error(w, "Нужен HTTPS-адрес обработчика общего бота", 400)
		return
	}
	if s.secrets == nil {
		http.Error(w, "Хранилище паролей недоступно", 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	link := telegramRelayLink{URL: in.URL}
	var reply telegramRelayReply
	if err := relayCall(ctx, link, "/pair/start", &reply); err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	u, err := url.Parse(reply.Link)
	if len(reply.Key) < 32 || err != nil || u.Scheme != "https" || u.Host != "t.me" || u.Path != "/songrequest_streamer_bot" {
		http.Error(w, "Неожиданный ответ общего бота", 502)
		return
	}
	link.Key = reply.Key
	if err := s.secrets.PutJSON(telegramRelayKey, link); err != nil {
		http.Error(w, "Не удалось сохранить привязку", 500)
		return
	}
	// Старый запуск прекращается до подтверждения новой учётной записи.
	if err := s.cfg.Update(func(c *config.Config) {
		c.Telegram.Enabled = false
		c.Telegram.Mode = "shared"
		c.Telegram.RelayURL = in.URL
		c.Telegram.OwnerID = 0
	}); err != nil {
		http.Error(w, "Не удалось сохранить настройки", 500)
		return
	}
	s.telegram.revision.Add(1)
	writeJSON(w, map[string]string{"link": reply.Link, "state": "waiting"})
}

func (s *Server) handleTelegramPairStatus(w http.ResponseWriter, r *http.Request) {
	link, err := s.relayLink()
	if err != nil {
		writeJSON(w, map[string]string{"state": "unpaired"})
		return
	}
	var reply telegramRelayReply
	if err := relayCall(r.Context(), link, "/pair/status", &reply); err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	reply.Key = ""
	reply.Link = ""
	writeJSON(w, reply)
}

func (s *Server) handleTelegramPairConfirm(w http.ResponseWriter, r *http.Request) {
	link, err := s.relayLink()
	if err != nil {
		http.Error(w, "Сначала начни подключение Telegram", 400)
		return
	}
	var reply telegramRelayReply
	if err := relayCall(r.Context(), link, "/pair/confirm", &reply); err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	if reply.State != "connected" || reply.OwnerID <= 0 {
		http.Error(w, "Telegram ещё не подтвердил привязку", 409)
		return
	}
	if err := s.cfg.Update(func(c *config.Config) {
		c.Telegram.Mode = "shared"
		c.Telegram.RelayURL = link.URL
		c.Telegram.OwnerID = reply.OwnerID
		c.Telegram.Enabled = true
	}); err != nil {
		http.Error(w, "Не удалось сохранить настройки", 500)
		return
	}
	s.telegram.revision.Add(1)
	writeJSON(w, map[string]any{"state": "connected", "owner_id": reply.OwnerID})
}

func (s *Server) handleTelegramPairRevoke(w http.ResponseWriter, r *http.Request) {
	if link, err := s.relayLink(); err == nil {
		if err := relayCall(r.Context(), link, "/pair/revoke", nil); err != nil {
			http.Error(w, "Обработчик недоступен. Повтори отключение позже или отправь /unlink боту.", 502)
			return
		}
	}
	if s.secrets != nil {
		if err := s.secrets.Delete(telegramRelayKey); err != nil {
			http.Error(w, "Не удалось удалить привязку", 500)
			return
		}
	}
	if err := s.cfg.Update(func(c *config.Config) { c.Telegram.Enabled = false; c.Telegram.OwnerID = 0 }); err != nil {
		http.Error(w, "Не удалось сохранить настройки", 500)
		return
	}
	s.telegram.revision.Add(1)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) telegramClient(cfg config.Telegram) (*tg.Client, error) {
	if cfg.Mode == "shared" {
		link, err := s.relayLink()
		if err != nil {
			return nil, err
		}
		return tg.NewRelay(link.URL, link.Key), nil
	}
	var token string
	if s.secrets == nil {
		return nil, fmt.Errorf("credential store unavailable")
	}
	if err := s.secrets.GetJSON(telegramKey, &token); err != nil || token == "" {
		return nil, fmt.Errorf("token missing")
	}
	return tg.New(token), nil
}
