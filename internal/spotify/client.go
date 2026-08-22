package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"

	"songrequest/internal/config"
	"songrequest/internal/errs"
	"songrequest/internal/logx"
	"songrequest/internal/secrets"
)

// SecretStore — хранилище токенов. Интерфейс, а не конкретный тип, чтобы
// тесты не лезли в «Диспетчер учётных данных» настоящей машины.
type SecretStore interface {
	PutJSON(name string, v any) error
	GetJSON(name string, v any) error
	Delete(name string) error
}

// Client — всё общение со Spotify. Один экземпляр на приложение.
type Client struct {
	cfg     *config.File
	log     *logx.Logger
	secrets SecretStore
	http    *http.Client

	// Адреса вынесены в поля, чтобы тесты подставляли свой сервер.
	tokenURL string
	apiBase  string

	mu          sync.RWMutex
	tokens      tokens
	pending     *pending
	redirectURI string
	me          *Me

	refreshMu sync.Mutex

	// OnStatus дёргается при смене состояния подключения — панель зажигает
	// лампочку. Заполняется снаружи, чтобы пакет не знал про интерфейс.
	OnStatus func(connected bool, detail string)
}

// Me — кто вошёл. Product важен: без Premium управление плеером не работает.
type Me struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Product     string `json:"product"`
	Country     string `json:"country"`
}

// Premium сообщает, есть ли подписка.
func (m *Me) Premium() bool { return m != nil && m.Product == "premium" }

// New создаёт клиент и подтягивает сохранённый вход, если он был.
func New(cfg *config.File, log *logx.Logger, sec SecretStore) *Client {
	c := &Client{
		cfg:      cfg,
		log:      log,
		secrets:  sec,
		tokenURL: "https://accounts.spotify.com/api/token",
		apiBase:  "https://api.spotify.com/v1",
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
	}

	var saved tokens
	switch err := sec.GetJSON(keyringName, &saved); {
	case err == nil:
		c.tokens = saved
		log.Redactor.Add(saved.AccessToken, saved.RefreshToken)
		log.Info("нашёл сохранённый вход в Spotify")
	case errors.Is(err, secrets.ErrNotFound):
		log.Debug("сохранённого входа в Spotify нет")
	default:
		log.Warn("не смог прочитать сохранённый вход", "ошибка", err)
	}
	return c
}

// Connected сообщает, есть ли рабочий вход (без похода в сеть).
func (c *Client) Connected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tokens.RefreshToken != ""
}

// Account отдаёт последние сведения о вошедшем аккаунте.
func (c *Client) Account() *Me {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.me
}

// status сообщает панели о смене состояния подключения.
func (c *Client) status(connected bool, detail string) {
	if c.OnStatus != nil {
		c.OnStatus(connected, detail)
	}
}

// CheckAccount спрашивает у Spotify, кто вошёл, и заодно проверяет Premium.
// Вызывается после входа и при старте приложения — это единственное место,
// где мы сами лезем в сеть без действия пользователя.
func (c *Client) CheckAccount(ctx context.Context) (*Me, error) {
	var me Me
	if err := c.do(ctx, http.MethodGet, "/me", nil, &me); err != nil {
		code, text := errs.Describe(err)
		c.status(false, text)
		c.log.Error("не смог получить данные аккаунта Spotify", "код", code, "ошибка", err)
		return nil, err
	}

	c.mu.Lock()
	c.me = &me
	c.mu.Unlock()

	if !me.Premium() {
		// Это не техническая ошибка, а бизнес-условие: без Premium Spotify
		// вообще не пускает к управлению плеером, и знать об этом надо сразу,
		// а не в момент первого заказа на стриме.
		err := errs.New(errs.SpotifyNoPremium,
			fmt.Sprintf("У аккаунта %s нет Spotify Premium. Без него приложение не сможет управлять музыкой.", me.DisplayName))
		c.status(false, err.UserText())
		c.log.Warn("аккаунт без Premium", "аккаунт", me.DisplayName, "подписка", me.Product)
		return &me, err
	}

	c.status(true, me.DisplayName+" · Premium")
	c.log.Info("Spotify подключён", "аккаунт", me.DisplayName, "страна", me.Country)
	return &me, nil
}

// do выполняет запрос к API: подставляет ключ доступа, повторяет при временных
// сбоях и переводит ответы Spotify в понятные ошибки с кодом.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}

	const maxAttempts = 4
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		token, err := c.token(ctx)
		if err != nil {
			return err
		}

		req, err := http.NewRequestWithContext(ctx, method, c.apiBase+path, bodyReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.http.Do(req)
		if err != nil {
			// Сеть моргнула — это самый частый сбой на домашнем интернете,
			// и он не должен превращаться в ошибку на стриме.
			lastErr = errs.Wrap(errs.SpotifyUnreachable, "Spotify не отвечает. Проверь интернет.", err)
			c.log.Warn("запрос к Spotify не дошёл", "путь", path, "попытка", attempt, "ошибка", err)
			if !sleepCtx(ctx, backoff(attempt)) {
				return lastErr
			}
			continue
		}

		data, readErr := readBody(resp)
		resp.Body.Close()
		if readErr != nil {
			lastErr = errs.Wrap(errs.SpotifyUnreachable, "Spotify оборвал ответ.", readErr)
			if !sleepCtx(ctx, backoff(attempt)) {
				return lastErr
			}
			continue
		}

		switch {
		case resp.StatusCode == http.StatusNoContent:
			// 204 означает две разные вещи. Если мы ждали данные — плеер молчит,
			// и это нормальный ответ, который разбирает вызывающий код. Если мы
			// отдавали команду (play, pause, шаффл) — 204 и есть «сделано».
			if out == nil {
				return nil
			}
			return errNoContent

		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			if out == nil || len(data) == 0 {
				return nil
			}
			if err := json.Unmarshal(data, out); err != nil {
				c.log.Error("не разобрал ответ Spotify", "путь", path, "ответ", string(data))
				return errs.Wrap(errs.SpotifyBadResponse, "Spotify ответил непонятным образом.", err)
			}
			return nil

		case resp.StatusCode == http.StatusUnauthorized:
			// Ключ протух раньше срока — выбрасываем его и идём на второй круг,
			// где token()сходит за новым.
			c.log.Debug("Spotify вернул 401, обновляю вход", "путь", path)
			c.mu.Lock()
			c.tokens.AccessToken = ""
			c.tokens.ExpiresAt = time.Time{}
			c.mu.Unlock()
			lastErr = errs.New(errs.SpotifyAuthExpired,
				"Слетела авторизация Spotify. Нажми «Подключить Spotify» заново.")
			continue

		case resp.StatusCode == http.StatusTooManyRequests:
			// Spotify сам говорит, сколько ждать. Уважаем и не спорим.
			wait := retryAfter(resp, attempt)
			c.log.Warn("Spotify просит подождать", "путь", path, "пауза", wait.String())
			lastErr = errs.New(errs.SpotifyRateLimit,
				"Spotify попросил сделать паузу. Приложение подождёт и попробует снова.")
			if !sleepCtx(ctx, wait) {
				return lastErr
			}
			continue

		case resp.StatusCode >= 500:
			c.log.Warn("Spotify отвечает ошибкой", "путь", path, "код_http", resp.StatusCode,
				"ответ", string(data), "попытка", attempt)
			lastErr = errs.New(errs.SpotifyUnreachable, "У Spotify временные неполадки. Пробую ещё раз.")
			if !sleepCtx(ctx, backoff(attempt)) {
				return lastErr
			}
			continue

		default:
			// 4xx кроме 401 и 429 повторять бессмысленно.
			c.log.Error("Spotify отказал", "путь", path, "метод", method,
				"код_http", resp.StatusCode, "ответ", string(data))
			return apiError(resp.StatusCode, data)
		}
	}

	return lastErr
}

// errNoContent — внутренний признак ответа 204 (плеер ничего не играет).
var errNoContent = errors.New("spotify: пустой ответ")

// apiError переводит отказ Spotify в понятную стримеру формулировку.
func apiError(statusCode int, data []byte) error {
	var e struct {
		Error struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
			Reason  string `json:"reason"`
		} `json:"error"`
	}
	json.Unmarshal(data, &e)

	switch {
	case e.Error.Reason == "NO_ACTIVE_DEVICE":
		return errs.New(errs.SpotifyNoDevice,
			"Spotify нигде не открыт. Запусти приложение Spotify и включи любой трек, чтобы устройство стало активным.")
	case e.Error.Reason == "PREMIUM_REQUIRED" || contains(e.Error.Message, "Premium"):
		return errs.New(errs.SpotifyNoPremium,
			"Для управления музыкой нужен Spotify Premium.")
	case statusCode == http.StatusForbidden:
		return errs.New(errs.SpotifyNoPremium,
			"Spotify запретил это действие. Чаще всего дело в отсутствии Premium или в том, что приложению не выдали нужные права при входе.")
	case statusCode == http.StatusNotFound:
		return errs.New(errs.SpotifyNoDevice,
			"Spotify не нашёл устройство или трек. Открой Spotify и включи любую песню, потом попробуй снова.")
	}
	return errs.New(errs.SpotifyBadResponse,
		fmt.Sprintf("Spotify отказал (%d). Подробности в логе.", statusCode))
}

// retryAfter читает заголовок Retry-After, а если его нет — берёт паузу сам.
func retryAfter(resp *http.Response, attempt int) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			// Секунда сверху: если проснуться ровно в срок, легко получить
			// второй отказ подряд из-за расхождения часов.
			return time.Duration(secs)*time.Second + time.Second
		}
	}
	return backoff(attempt)
}

// backoff — задержка с ростом и разбросом. Разброс нужен, чтобы несколько
// одновременных запросов не пошли на повтор одной шеренгой.
func backoff(attempt int) time.Duration {
	base := time.Duration(1<<uint(attempt-1)) * 500 * time.Millisecond
	if base > 8*time.Second {
		base = 8 * time.Second
	}
	return base + time.Duration(rand.Int63n(int64(250*time.Millisecond)))
}

// sleepCtx ждёт d; false означает, что приложение закрывают.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func bodyReader(payload []byte) io.Reader {
	if payload == nil {
		return nil
	}
	return bytes.NewReader(payload)
}

// readBody читает ответ целиком, но с потолком: сломанный или враждебный
// ответ не должен съесть всю память.
func readBody(resp *http.Response) ([]byte, error) {
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && bytes.Contains([]byte(haystack), []byte(needle))
}
