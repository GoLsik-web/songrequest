package twitch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/coder/websocket"

	"songrequest/internal/errs"
)

// eventSubURL — адрес WebSocket EventSub. Именно WebSocket, а не webhook:
// у приложения на домашнем компьютере нет публичного адреса, куда Twitch
// мог бы стучаться.
const eventSubURL = "wss://eventsub.wss.twitch.tv/ws"

// Redemption — заказ, который сделал зритель.
type Redemption struct {
	ID          string    `json:"id"`
	RewardID    string    `json:"reward_id"`
	RewardTitle string    `json:"reward_title"`
	RewardCost  int       `json:"reward_cost"`
	UserID      string    `json:"user_id"`
	UserLogin   string    `json:"user_login"`
	UserName    string    `json:"user_name"`
	UserInput   string    `json:"user_input"`
	RedeemedAt  time.Time `json:"redeemed_at"`
}

// EventSub держит подписку на события канала.
type EventSub struct {
	client *Client

	// OnRedemption вызывается на каждый заказ за баллы.
	OnRedemption func(Redemption)
	// OnStatus сообщает панели, жива ли подписка.
	OnStatus func(connected bool, detail string)

	wsURL string
}

// NewEventSub создаёт подписку.
func NewEventSub(c *Client) *EventSub {
	url := eventSubURL
	if base := os.Getenv("SONGREQUEST_TWITCH_WS"); base != "" {
		url = base
	}
	return &EventSub{client: c, wsURL: url}
}

// Run держит соединение живым до отмены контекста.
//
// Соединение рвётся по десятку причин — от перезагрузки роутера до плановой
// переброски на другой сервер Twitch. Стример об этом знать не должен:
// переподключаемся сами, с растущей паузой, и заново оформляем подписку.
func (e *EventSub) Run(ctx context.Context, rewardID string) {
	attempt := 0

	for ctx.Err() == nil {
		err := e.session(ctx, rewardID)
		if ctx.Err() != nil {
			return
		}

		attempt++
		wait := backoff(min(attempt, 5))
		if err != nil {
			e.client.log.Warn("подписка на события Twitch оборвалась",
				"ошибка", err, "повтор_через", wait.String())
			e.status(false, string(errs.TwitchEventSub)+" · переподключаюсь")
		}
		if !sleepCtx(ctx, wait) {
			return
		}
	}
}

// session проживает одно соединение от приветствия до разрыва.
func (e *EventSub) session(ctx context.Context, rewardID string) error {
	url := e.wsURL

	// Twitch может попросить переехать на другой адрес прямо посреди работы;
	// подписки при этом переносятся сами, оформлять их заново не нужно.
	for {
		conn, _, err := websocket.Dial(ctx, url, nil)
		if err != nil {
			return fmt.Errorf("не подключился к событиям Twitch: %w", err)
		}

		// Сообщения бывают большими: у заказа есть текст зрителя.
		conn.SetReadLimit(1 << 20)

		next, err := e.pump(ctx, conn, url == e.wsURL, rewardID)
		conn.CloseNow()

		if err != nil {
			return err
		}
		if next == "" {
			return nil
		}
		e.client.log.Info("Twitch попросил переехать на другой адрес")
		url = next
	}
}

// pump читает сообщения одного соединения. Возвращает адрес для переезда,
// если Twitch попросил переподключиться.
func (e *EventSub) pump(ctx context.Context, conn *websocket.Conn, subscribe bool, rewardID string) (string, error) {
	// Пока не пришло приветствие, срок молчания неизвестен — берём с запасом.
	keepalive := 30 * time.Second

	for {
		readCtx, cancel := context.WithTimeout(ctx, keepalive+15*time.Second)
		_, data, err := conn.Read(readCtx)
		cancel()
		if err != nil {
			return "", err
		}

		var msg struct {
			Metadata struct {
				Type             string `json:"message_type"`
				SubscriptionType string `json:"subscription_type"`
			} `json:"metadata"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			e.client.log.Warn("непонятное сообщение от Twitch", "сообщение", string(data))
			continue
		}

		switch msg.Metadata.Type {
		case "session_welcome":
			var p struct {
				Session struct {
					ID      string `json:"id"`
					Timeout int    `json:"keepalive_timeout_seconds"`
				} `json:"session"`
			}
			if err := json.Unmarshal(msg.Payload, &p); err != nil {
				return "", err
			}
			if p.Session.Timeout > 0 {
				keepalive = time.Duration(p.Session.Timeout) * time.Second
			}

			if subscribe {
				// Подписку надо оформить быстро: Twitch рвёт неиспользуемое
				// соединение через десять секунд после приветствия.
				if err := e.subscribe(ctx, p.Session.ID, rewardID); err != nil {
					return "", err
				}
			}
			e.status(true, "заказы принимаются")
			e.client.log.Info("подписка на заказы Twitch активна", "молчание_до", keepalive.String())

		case "session_keepalive":
			// Тишина в эфире — соединение живо, делать нечего.

		case "session_reconnect":
			var p struct {
				Session struct {
					ReconnectURL string `json:"reconnect_url"`
				} `json:"session"`
			}
			if err := json.Unmarshal(msg.Payload, &p); err != nil {
				return "", err
			}
			return p.Session.ReconnectURL, nil

		case "revocation":
			// Twitch отозвал подписку: стример отключил приложение или
			// удалил награду. Переподключение не поможет, нужен вход заново.
			e.client.log.Warn("Twitch отозвал подписку на заказы", "ответ", string(msg.Payload))
			e.status(false, string(errs.TwitchEventSub)+" · подписка отозвана, подключись заново")
			return "", errs.New(errs.TwitchEventSub,
				"Twitch отозвал доступ к заказам. Нажми «Подключить Twitch» заново.")

		case "notification":
			e.handleNotification(msg.Payload)

		default:
			e.client.log.Debug("неизвестный тип сообщения", "тип", msg.Metadata.Type)
		}
	}
}

// handleNotification разбирает событие заказа.
func (e *EventSub) handleNotification(payload json.RawMessage) {
	var p struct {
		Event struct {
			ID         string    `json:"id"`
			UserID     string    `json:"user_id"`
			UserLogin  string    `json:"user_login"`
			UserName   string    `json:"user_name"`
			UserInput  string    `json:"user_input"`
			Status     string    `json:"status"`
			RedeemedAt time.Time `json:"redeemed_at"`
			Reward     struct {
				ID    string `json:"id"`
				Title string `json:"title"`
				Cost  int    `json:"cost"`
			} `json:"reward"`
		} `json:"event"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		e.client.log.Warn("не разобрал событие заказа", "событие", string(payload))
		return
	}

	r := Redemption{
		ID:          p.Event.ID,
		RewardID:    p.Event.Reward.ID,
		RewardTitle: p.Event.Reward.Title,
		RewardCost:  p.Event.Reward.Cost,
		UserID:      p.Event.UserID,
		UserLogin:   p.Event.UserLogin,
		UserName:    p.Event.UserName,
		// Текст чистим сразу: зрители вставляют его из буфера обмена вместе с
		// переводами строк и лишними пробелами, а дальше по нему будет
		// искаться трек, и невидимый символ в начале сломает поиск.
		UserInput:  cleanInput(p.Event.UserInput),
		RedeemedAt: p.Event.RedeemedAt,
	}

	e.client.log.Info("заказ за баллы",
		"зритель", r.UserLogin, "текст", r.UserInput, "id", r.ID)

	if e.OnRedemption != nil {
		e.OnRedemption(r)
	}
}

// cleanInput убирает из текста заказа переводы строк, табуляции и повторные
// пробелы. Twitch отдаёт то, что ввёл зритель, слово в слово.
func cleanInput(s string) string {
	// Переводы строк, табуляции и прочие управляющие символы превращаем в
	// пробелы, а затем схлопываем пробелы. strings.Fields делает это сам.
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// subscribe оформляет подписку на заказы за баллы.
func (e *EventSub) subscribe(ctx context.Context, sessionID, rewardID string) error {
	user := e.client.Account()
	if user == nil {
		return errs.New(errs.TwitchAuthExpired, "Сначала подключи Twitch.")
	}

	condition := map[string]any{"broadcaster_user_id": user.ID}
	// Фильтр по награде: без него посыплются события от всех наград канала,
	// включая чужие, за которые мы даже баллы вернуть не можем.
	if rewardID != "" {
		condition["reward_id"] = rewardID
	}

	body := map[string]any{
		"type":      "channel.channel_points_custom_reward_redemption.add",
		"version":   "1",
		"condition": condition,
		"transport": map[string]any{
			"method":     "websocket",
			"session_id": sessionID,
		},
	}

	if err := e.client.do(ctx, http.MethodPost, "/eventsub/subscriptions", body, nil); err != nil {
		return errs.Wrap(errs.TwitchEventSub, "Не получилось подписаться на заказы.", err)
	}
	return nil
}

func (e *EventSub) status(connected bool, detail string) {
	if e.OnStatus != nil {
		e.OnStatus(connected, detail)
	}
}
