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
	"strings"
	"sync"
	"sync/atomic"
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

	// httpClient меняется на ходу: стример включает прокси прямо во время
	// стрима, а запросы в это время идут из горутин плеера и подбора. Голое
	// поле здесь — гонка, поэтому только через client()/setHTTP.
	httpClient atomic.Pointer[http.Client]

	// Адреса вынесены в поля, чтобы тесты подставляли свой сервер.
	tokenURL string
	apiBase  string

	mu          sync.RWMutex
	tokens      tokens
	pending     *pending
	redirectURI string
	me          *Me
	// checked и checkErr — чем кончилась последняя проверка аккаунта.
	// Нужны панели: без них красная лампочка не может назвать код.
	checked  bool
	checkErr error
	// proxyLabel — через кого ходим к Spotify, без пароля.
	proxyLabel string

	refreshMu sync.Mutex
}

// Me — кто вошёл в Spotify.
type Me struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	// Product приходит только при выданном праве user-read-private.
	// Пустая строка означает «Spotify не сказал», а не «подписки нет» —
	// путать эти два случая нельзя, из-за этого приложение врало людям.
	Product string `json:"product"`
	Country string `json:"country"`
}

// Plan — что мы знаем о подписке. Именно три состояния, а не «да/нет».
type Plan string

const (
	// PlanPremium — Premium подтверждён, всё будет работать.
	PlanPremium Plan = "premium"
	// PlanFree — Spotify прямо сказал, что подписки нет.
	PlanFree Plan = "free"
	// PlanUnknown — определить не удалось. Не повод блокировать работу:
	// если Premium на самом деле есть, всё заработает, а если нет — Spotify
	// сам откажет при первой команде плееру, и мы это покажем.
	PlanUnknown Plan = "unknown"
)

// Plan разбирает ответ Spotify о подписке.
func (m *Me) Plan() Plan {
	switch {
	case m == nil || strings.TrimSpace(m.Product) == "":
		return PlanUnknown
	case strings.HasPrefix(m.Product, "premium"):
		// Spotify не различает Standard, Duo и Family — все они premium.
		return PlanPremium
	default:
		return PlanFree
	}
}

// PlanLabel — то, что показываем стримеру в панели.
func (m *Me) PlanLabel() string {
	switch m.Plan() {
	case PlanPremium:
		return "Premium"
	case PlanFree:
		return "без подписки"
	default:
		return "подписка не определена"
	}
}

// Premium сообщает, точно ли есть подписка.
func (m *Me) Premium() bool { return m.Plan() == PlanPremium }

// New создаёт клиент и подтягивает сохранённый вход, если он был.
func New(cfg *config.File, log *logx.Logger, sec SecretStore) *Client {
	c := &Client{
		cfg:      cfg,
		log:      log,
		secrets:  sec,
		tokenURL: "https://accounts.spotify.com/api/token",
		apiBase:  "https://api.spotify.com/v1",
	}
	c.setHTTP(&http.Client{Timeout: 15 * time.Second})

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

// LastCheck сообщает, чем кончилась последняя проверка аккаунта.
//
// done=false означает, что проверка ещё ни разу не доходила до конца — при
// запуске это обычное дело, и показывать в этот момент красную лампочку
// нечестно. Ошибка нужна панели, чтобы назвать код: без кода человек может
// сказать мне только «не работает».
func (c *Client) LastCheck() (done bool, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.checked, c.checkErr
}

// Страны, где Spotify не работает. Список короткий и меняется редко, но
// именно он объясняет самый непонятный случай: вход есть, Premium есть,
// а поиск пустой и в плейлистах ноль треков.
var deadMarkets = map[string]string{
	"RU": "России",
	"BY": "Беларуси",
}

// MarketProblem объясняет, если страна аккаунта делает Spotify бесполезным.
// Пусто — значит со страной всё в порядке.
func MarketProblem(country string) string {
	where, dead := deadMarkets[strings.ToUpper(strings.TrimSpace(country))]
	if !dead {
		return ""
	}
	return "Аккаунт зарегистрирован в " + where + ", а Spotify там не работает. " +
		"Поиск будет находить мало или ничего, а плейлисты покажут ноль треков — " +
		"даже с включённым VPN: Spotify смотрит на страну аккаунта, а не на адрес. " +
		"Лечится только сменой страны в настройках аккаунта Spotify."
}

// GrantedScopes — права, которые стример выдал при входе.
func (c *Client) GrantedScopes() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tokens.Scope
}

// hasScope проверяет одно право.
func (c *Client) hasScope(scope string) bool {
	return strings.Contains(c.GrantedScopes(), scope)
}

// PlanProblem объясняет, почему подписку не удалось подтвердить.
// nil означает, что с подпиской всё в порядке.
func (c *Client) PlanProblem(me *Me) *errs.Error {
	switch me.Plan() {
	case PlanPremium:
		return nil

	case PlanFree:
		return errs.New(errs.SpotifyNoPremium,
			"Spotify сообщил, что на этом аккаунте нет Premium. Управлять музыкой не получится — проверь, тем ли аккаунтом вошёл.")

	default:
		// Разделяем две причины «не определили»: не выдано право (лечится
		// одной кнопкой) и Spotify промолчал (лечится ожиданием).
		if !c.hasScope("user-read-private") {
			return errs.New(errs.SpotifyPlanUnknown,
				"Не могу проверить подписку: при входе не выдано право читать данные аккаунта. Нажми «Подключить заново» — приложение попросит его и всё определится.")
		}
		return errs.New(errs.SpotifyPlanUnknown,
			"Spotify не сообщил тип подписки. Работать можно: если Premium есть, всё заработает, а если нет — Spotify откажет при первом заказе, и я об этом скажу.")
	}
}

// CheckAccount спрашивает у Spotify, кто вошёл, и запоминает ответ.
//
// Ошибку возвращает только на настоящий сбой связи. Про подписку решение
// принимает вызывающий код через PlanProblem: «не смогли определить» — это
// не отказ, и блокировать из-за него работу нельзя.
func (c *Client) CheckAccount(ctx context.Context) (*Me, error) {
	// Забираем ответ и разбираем его, и целиком кладём в лог: когда у чужого
	// человека «нет Premium» при живой подписке, разбираться приходится
	// именно по этому куску JSON.
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/me", nil, &raw); err != nil {
		code, _ := errs.Describe(err)
		c.log.Error("не смог получить данные аккаунта Spotify", "код", code, "ошибка", err)
		c.rememberCheck(err)
		return nil, err
	}

	var me Me
	if err := json.Unmarshal(raw, &me); err != nil {
		c.log.Error("не разобрал ответ о аккаунте", "ответ", string(raw))
		wrapped := errs.Wrap(errs.SpotifyBadResponse, "Spotify ответил непонятным образом.", err)
		c.rememberCheck(wrapped)
		return nil, wrapped
	}

	c.mu.Lock()
	c.me = &me
	c.checked = true
	c.checkErr = nil
	c.mu.Unlock()

	fields := []any{
		"аккаунт", me.DisplayName,
		"подписка_от_spotify", me.Product,
		"страна", me.Country,
		"выданные_права", c.GrantedScopes(),
		"ответ", string(raw),
	}
	if me.Plan() == PlanUnknown {
		// Это ровно тот случай, ради которого лог и читают, поэтому он не
		// прячется за подробным режимом.
		c.log.Warn("Spotify не сообщил тип подписки", fields...)
	} else {
		c.log.Info("данные аккаунта Spotify получены", fields...)
	}

	// Почту в панели показываем, а из лога вычищаем — архив уходит наружу.
	c.log.Redactor.Add(me.Email)
	return &me, nil
}

// rememberCheck запоминает неудачу, чтобы панель назвала код, а не разводила
// руками.
func (c *Client) rememberCheck(err error) {
	c.mu.Lock()
	c.checked = true
	c.checkErr = err
	c.mu.Unlock()
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

		resp, err := c.client().Do(req)
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
			// Spotify сам говорит, сколько ждать. Уважаем — но не до бесконечности.
			//
			// При блокировке приложения Spotify просит подождать тысячи секунд.
			// Раньше это отсыпалось как есть, четыре раза подряд, и очередь на
			// стриме просто вставала на часы без единого слова в панели.
			wait := retryAfter(resp, attempt)
			if wait > maxRateWait {
				c.log.Error("Spotify просит слишком долгую паузу, ждать не будем",
					"путь", path, "пауза", wait.String())
				return errs.New(errs.SpotifyRateLimit,
					"Spotify временно ограничил приложение и просит долгую паузу ("+
						wait.Round(time.Second).String()+"). Музыка вернётся сама, когда он её снимет.")
			}
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
			return apiError(resp.StatusCode, path, data)
		}
	}

	return lastErr
}

// errNoContent — внутренний признак ответа 204 (плеер ничего не играет).
var errNoContent = errors.New("spotify: пустой ответ")

// apiError переводит отказ Spotify в понятную стримеру формулировку.
func apiError(statusCode int, path string, data []byte) error {
	var e struct {
		Error struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
			Reason  string `json:"reason"`
		} `json:"error"`
	}
	json.Unmarshal(data, &e)

	switch {
	// Spotify отвечает этим, когда страна аккаунта и страна выхода в интернет
	// расходятся, — обычно из-за отключившегося VPN. К подписке это отношения
	// не имеет, и говорить человеку про Premium тут просто неправда.
	case contains(e.Error.Message, "unavailable in this country"):
		return errs.New(errs.SpotifyCountry,
			"Spotify не работает из этой страны. Если пользуешься VPN — включи его и нажми «Проверить связь».")

	case e.Error.Reason == "NO_ACTIVE_DEVICE":
		return errs.New(errs.SpotifyNoDevice,
			"Spotify нигде не открыт. Запусти приложение Spotify и включи любой трек, чтобы устройство стало активным.")
	case e.Error.Reason == "PREMIUM_REQUIRED" || contains(e.Error.Message, "Premium"):
		return errs.New(errs.SpotifyNoPremium,
			"Для управления музыкой нужен Spotify Premium.")
	case statusCode == http.StatusForbidden:
		return errs.New(errs.SpotifyNoPremium,
			"Spotify запретил это действие. Чаще всего дело в отсутствии Premium или в том, что приложению не выдали нужные права при входе.")
	case statusCode == http.StatusNotFound && strings.HasPrefix(path, "/me/player"):
		return errs.New(errs.SpotifyNoDevice,
			"Spotify не нашёл устройство. Открой Spotify и включи любую песню, потом попробуй снова.")
	case statusCode == http.StatusNotFound:
		// Совет «открой Spotify» здесь не поможет никогда: чаще всего это
		// удалённый запасной плейлист или трек, которого больше нет.
		return errs.New(errs.SpotifyNothing,
			"Spotify не нашёл того, что мы просим: плейлист или трек удалён. Выбери другой.")
	}
	return errs.New(errs.SpotifyBadResponse,
		fmt.Sprintf("Spotify отказал (%d). Подробности в логе.", statusCode))
}

// maxRateWait — самая долгая пауза, которую готовы отсидеть. Тридцать секунд
// на стриме уже слышно, а всё, что дольше, — это не «подожди», а «приходи
// потом»: об этом надо сказать человеку, а не молчать в спящей горутине.
const maxRateWait = 30 * time.Second

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

// client отдаёт текущий транспорт: прямой или через прокси.
func (c *Client) client() *http.Client {
	if h := c.httpClient.Load(); h != nil {
		return h
	}
	return http.DefaultClient
}

// setHTTP подменяет транспорт целиком — так включается и выключается прокси.
func (c *Client) setHTTP(h *http.Client) { c.httpClient.Store(h) }
