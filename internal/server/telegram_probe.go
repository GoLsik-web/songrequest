package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"songrequest/internal/i18n"
	tg "songrequest/internal/telegram"
)

var errTelegramWebhook = errors.New("У бота включён webhook. Используй отдельного бота для приложения.")
var errTelegramSupportBot = errors.New("Бот @songrequest_wrapped_bot предназначен только для поддержки. Создай отдельного личного бота через @BotFather.")
var errTelegramSharedBot = errors.New("Общий @songrequest_streamer_bot подключается кнопкой без токена. Выбери общий бот в настройках.")

func telegramTestMessage(ctx context.Context, c *tg.Client, owner int64) (string, error) {
	var me struct {
		Username string `json:"username"`
	}
	if err := c.Call(ctx, "getMe", map[string]any{}, &me); err != nil {
		return "", err
	}
	if strings.EqualFold(me.Username, "songrequest_wrapped_bot") {
		return "", errTelegramSupportBot
	}
	if c.RelayKey == "" && strings.EqualFold(me.Username, "songrequest_streamer_bot") {
		return "", errTelegramSharedBot
	}
	var webhook struct {
		URL string `json:"url"`
	}
	if err := c.Call(ctx, "getWebhookInfo", map[string]any{}, &webhook); err != nil {
		return "", err
	}
	if webhook.URL != "" {
		return "", errTelegramWebhook
	}
	if err := c.Call(ctx, "sendMessage", map[string]any{"chat_id": owner, "text": "✅ Проверка songrequest: бот может присылать тебе отчёты. Отправь /start, чтобы открыть меню."}, nil); err != nil {
		return "", err
	}
	return me.Username, nil
}

func (s *Server) handleTelegramTest(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg.Get().Telegram
	if !cfg.Enabled || cfg.OwnerID <= 0 {
		http.Error(w, i18n.T("Сначала включи Telegram, укажи свой ID и сохрани настройки."), 400)
		return
	}
	client, err := s.telegramClient(cfg)
	if err != nil {
		http.Error(w, i18n.T("Укажи токен бота"), 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	username, err := telegramTestMessage(ctx, client, cfg.OwnerID)
	if err != nil {
		message := telegramErrorNote(err)
		if errors.Is(err, errTelegramWebhook) || errors.Is(err, errTelegramSupportBot) || errors.Is(err, errTelegramSharedBot) {
			message = err.Error()
		}
		http.Error(w, i18n.T(message), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "username": username})
}
