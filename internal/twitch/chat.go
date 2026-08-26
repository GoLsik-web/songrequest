package twitch

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"songrequest/internal/errs"
)

// ChatMessage — сообщение из чата канала.
type ChatMessage struct {
	Login       string
	DisplayName string
	Text        string
	// IsBroadcaster и IsModerator решают, слушать ли команду. Обычным
	// зрителям команды недоступны: заказ идёт только через награду или донат.
	IsBroadcaster bool
	IsModerator   bool
}

// CanCommand сообщает, вправе ли автор отдавать команды.
func (m ChatMessage) CanCommand() bool { return m.IsBroadcaster || m.IsModerator }

// Say пишет в чат канала от имени вошедшего аккаунта.
func (c *Client) Say(ctx context.Context, text string) error {
	user := c.Account()
	if user == nil {
		return errs.New(errs.TwitchAuthExpired, "Twitch не подключён.")
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	// Twitch режет длинные сообщения молча — лучше обрезать самим и понятно.
	if len([]rune(text)) > 480 {
		text = string([]rune(text)[:477]) + "…"
	}

	body := map[string]any{
		"broadcaster_id": user.ID,
		"sender_id":      user.ID,
		"message":        text,
	}
	// Ответ обязательно разбираем. Twitch отвечает «200 ОК» и на сообщение,
	// которое он не отправил: задержал AutoMod, включён режим «только для
	// фолловеров», слоумод, эмоут-онли. Раньше приложение считало такое
	// успехом — и все ответы зрителям («принято, третьим в очереди», «баллы
	// вернул», «трека нет») молча пропадали весь стрим, а в панели и в логе
	// было зелено. Зрители при этом жаловались, что бот молчит и «баллы
	// съело».
	var out struct {
		Data []struct {
			IsSent     bool `json:"is_sent"`
			DropReason *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"drop_reason"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/chat/messages", body, &out); err != nil {
		c.log.Warn("не написал в чат", "текст", text, "ошибка", err)
		return err
	}
	if len(out.Data) > 0 && !out.Data[0].IsSent {
		why := "Twitch не сказал причину"
		if r := out.Data[0].DropReason; r != nil {
			why = r.Code
			if r.Message != "" {
				why += " · " + r.Message
			}
		}
		c.log.Warn("Twitch не пропустил сообщение в чат", "текст", text, "причина", why)
		return errs.New(errs.TwitchChat, "Twitch не пропустил сообщение в чат: "+why)
	}
	return nil
}

// subscribeChat подписывает соединение EventSub на сообщения чата.
func (e *EventSub) subscribeChat(ctx context.Context, sessionID string) error {
	user := e.client.Account()
	if user == nil {
		return errs.New(errs.TwitchAuthExpired, "Сначала подключи Twitch.")
	}

	body := map[string]any{
		"type":    "channel.chat.message",
		"version": "1",
		"condition": map[string]any{
			"broadcaster_user_id": user.ID,
			"user_id":             user.ID,
		},
		"transport": map[string]any{
			"method":     "websocket",
			"session_id": sessionID,
		},
	}

	if err := e.client.do(ctx, http.MethodPost, "/eventsub/subscriptions", body, nil); err != nil {
		// Чат — не критично: заказы за баллы работают и без него. Поэтому
		// не роняем всю подписку, а говорим в лог и живём дальше.
		e.client.log.Warn("не подписался на чат, команды работать не будут", "ошибка", err)
		return nil
	}
	return nil
}

// handleChat разбирает сообщение чата.
func (e *EventSub) handleChat(payload []byte) {
	var p struct {
		Event struct {
			BroadcasterUserID string `json:"broadcaster_user_id"`
			ChatterUserID     string `json:"chatter_user_id"`
			ChatterUserLogin  string `json:"chatter_user_login"`
			ChatterUserName   string `json:"chatter_user_name"`
			Message           struct {
				Text string `json:"text"`
			} `json:"message"`
			Badges []struct {
				SetID string `json:"set_id"`
			} `json:"badges"`
		} `json:"event"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return
	}

	msg := ChatMessage{
		Login:       p.Event.ChatterUserLogin,
		DisplayName: p.Event.ChatterUserName,
		Text:        cleanInput(p.Event.Message.Text),
	}
	// Значки — самый надёжный признак прав: они приходят вместе с сообщением,
	// и лишний запрос к API на каждое слово в чате делать не нужно.
	for _, b := range p.Event.Badges {
		switch b.SetID {
		case "broadcaster":
			msg.IsBroadcaster = true
		case "moderator":
			msg.IsModerator = true
		}
	}

	if e.OnChat != nil {
		// В отдельной горутине по той же причине, что и заказы: команда из
		// чата делает сетевые вызовы, а цикл чтения сокета в это время
		// стоять не должен.
		go e.OnChat(msg)
	}
}
