// Package spotify — вход в Spotify и управление воспроизведением стримера.
package spotify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"songrequest/internal/errs"
)

const (
	authorizeURL = "https://accounts.spotify.com/authorize"
	keyringName  = "spotify"
)

// scopes — ровно то, что нужно, и ничего сверх: читать состояние плеера,
// управлять им и видеть текущий трек. Лишние права стример справедливо
// воспринимает как повод не устанавливать приложение.
var scopes = []string{
	"user-read-playback-state",
	"user-modify-playback-state",
	"user-read-currently-playing",
}

// tokens — то, что лежит в хранилище учётных данных.
type tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scope        string    `json:"scope"`
}

func (t tokens) valid() bool {
	// Минута запаса: токен, который истечёт через десять секунд, считаем
	// протухшим, иначе запрос уйдёт и вернётся с 401 на ровном месте.
	return t.AccessToken != "" && time.Now().Add(time.Minute).Before(t.ExpiresAt)
}

// pending — начатый, но не завершённый вход.
type pending struct {
	state    string
	verifier string
	started  time.Time
}

// tokenResponse — ответ Spotify на выдачу и обновление токена.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// AuthURL начинает вход: генерирует PKCE-пару и отдаёт ссылку, которую надо
// открыть в браузере. redirectURI обязан совпадать с тем, что вписан в
// настройках приложения на developer.spotify.com, символ в символ.
func (c *Client) AuthURL(redirectURI string) (string, error) {
	cfg := c.cfg.Get()
	if strings.TrimSpace(cfg.SpotifyClientID) == "" {
		return "", errs.New(errs.NoClientID,
			"Не заполнен Client ID Spotify. Открой настройки и вставь его — как его получить, написано в инструкции.")
	}

	verifier, err := randomString(64)
	if err != nil {
		return "", errs.Wrap(errs.SpotifyAuthStart, "Не получилось начать вход в Spotify.", err)
	}
	state, err := randomString(24)
	if err != nil {
		return "", errs.Wrap(errs.SpotifyAuthStart, "Не получилось начать вход в Spotify.", err)
	}

	// code_challenge = base64url(sha256(verifier)) без padding — так требует S256.
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	c.mu.Lock()
	c.pending = &pending{state: state, verifier: verifier, started: time.Now()}
	c.redirectURI = redirectURI
	c.mu.Unlock()

	// Сам verifier в лог не пишем, но пусть редактор знает его на случай,
	// если он всплывёт внутри чужого сообщения об ошибке.
	c.log.Redactor.Add(verifier)

	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {cfg.SpotifyClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {strings.Join(scopes, " ")},
		"code_challenge_method": {"S256"},
		"code_challenge":        {challenge},
		"state":                 {state},
	}
	c.log.Info("начинаю вход в Spotify", "redirect_uri", redirectURI)
	return authorizeURL + "?" + q.Encode(), nil
}

// Complete завершает вход: меняет код из браузера на токены и сохраняет их.
func (c *Client) Complete(ctx context.Context, code, state string) error {
	c.mu.Lock()
	p := c.pending
	redirectURI := c.redirectURI
	c.pending = nil
	c.mu.Unlock()

	if p == nil {
		return errs.New(errs.SpotifyAuthDenied,
			"Вход не начинался или страница провисела слишком долго. Нажми «Подключить Spotify» ещё раз.")
	}
	// Несовпадение state значит, что ответ пришёл не на наш запрос: либо
	// перепутаны вкладки, либо кто-то подсовывает чужой код. Продолжать нельзя.
	if state != p.state {
		return errs.New(errs.SpotifyAuthDenied,
			"Ответ Spotify не совпал с запросом. Нажми «Подключить Spotify» ещё раз и не держи две вкладки входа сразу.")
	}
	if time.Since(p.started) > 10*time.Minute {
		return errs.New(errs.SpotifyAuthDenied,
			"Страница входа провисела слишком долго. Нажми «Подключить Spotify» ещё раз.")
	}

	cfg := c.cfg.Get()
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {cfg.SpotifyClientID},
		"code_verifier": {p.verifier},
	}

	tr, err := c.postToken(ctx, form)
	if err != nil {
		return err
	}

	t := tokens{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
		Scope:        tr.Scope,
	}
	if err := c.saveTokens(t); err != nil {
		return errs.Wrap(errs.SpotifyAuthToken, "Вход прошёл, но сохранить его не удалось.", err)
	}
	c.log.Info("вход в Spotify выполнен")
	return nil
}

// token отдаёт годный ключ доступа, при необходимости обновляя его.
// Обновление под отдельным мьютексом: десять параллельных запросов не должны
// устроить десять гонок за новым токеном.
func (c *Client) token(ctx context.Context) (string, error) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	c.mu.RLock()
	t := c.tokens
	c.mu.RUnlock()

	if t.valid() {
		return t.AccessToken, nil
	}
	if t.RefreshToken == "" {
		return "", errs.New(errs.SpotifyAuthExpired,
			"Spotify не подключён. Нажми «Подключить Spotify» в панели.")
	}

	cfg := c.cfg.Get()
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {t.RefreshToken},
		"client_id":     {cfg.SpotifyClientID},
	}

	tr, err := c.postToken(ctx, form)
	if err != nil {
		// Обновиться не вышло — вход слетел совсем. Это надо сказать прямо,
		// а не показывать «401» и оставлять человека гадать.
		if errs.CodeOf(err) == errs.SpotifyAuthToken {
			c.forgetTokens()
			return "", errs.Wrap(errs.SpotifyAuthExpired,
				"Слетела авторизация Spotify. Нажми «Подключить Spotify» заново.", err)
		}
		return "", err
	}

	updated := tokens{
		AccessToken: tr.AccessToken,
		// Новый refresh_token приходит не всегда: если его нет, продолжаем
		// пользоваться прежним, иначе разлогинимся на ровном месте.
		RefreshToken: firstNonEmpty(tr.RefreshToken, t.RefreshToken),
		ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
		Scope:        firstNonEmpty(tr.Scope, t.Scope),
	}
	if err := c.saveTokens(updated); err != nil {
		c.log.Warn("не сохранил обновлённый вход", "ошибка", err)
	}
	c.log.Debug("ключ доступа Spotify обновлён")
	return updated.AccessToken, nil
}

// postToken выполняет запрос к /api/token и разбирает ответ.
func (c *Client) postToken(ctx context.Context, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errs.Wrap(errs.SpotifyAuthToken, "Не получилось обратиться к Spotify.", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errs.Wrap(errs.SpotifyUnreachable,
			"Spotify не отвечает. Проверь интернет и попробуй ещё раз.", err)
	}
	defer resp.Body.Close()

	body, err := readBody(resp)
	if err != nil {
		return nil, errs.Wrap(errs.SpotifyUnreachable, "Spotify оборвал ответ. Попробуй ещё раз.", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Полный ответ — в лог: без него разбирать чужую проблему невозможно.
		// Секреты из него вычистит редактор в logx.
		c.log.Error("Spotify отказал во входе", "код_http", resp.StatusCode, "ответ", string(body))
		return nil, errs.New(errs.SpotifyAuthToken, tokenErrorText(body))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		c.log.Error("не разобрал ответ Spotify", "ответ", string(body))
		return nil, errs.Wrap(errs.SpotifyBadResponse, "Spotify ответил непонятным образом.", err)
	}
	if tr.AccessToken == "" {
		return nil, errs.New(errs.SpotifyBadResponse, "Spotify не выдал ключ доступа.")
	}

	c.log.Redactor.Add(tr.AccessToken, tr.RefreshToken)
	return &tr, nil
}

// tokenErrorText переводит ошибку авторизации на человеческий язык.
func tokenErrorText(body []byte) string {
	var e struct {
		Error     string `json:"error"`
		ErrorDesc string `json:"error_description"`
	}
	json.Unmarshal(body, &e)

	switch e.Error {
	case "invalid_client":
		return "Spotify не узнал Client ID. Проверь, что он вставлен целиком и без пробелов."
	case "invalid_grant":
		return "Spotify отклонил вход. Чаще всего это значит, что адрес в настройках приложения Spotify отличается от нужного хотя бы одним символом."
	case "invalid_request":
		return fmt.Sprintf("Spotify не принял запрос на вход (%s).", firstNonEmpty(e.ErrorDesc, "без объяснения"))
	}
	return "Не удалось завершить вход в Spotify. Попробуй ещё раз, а если не выйдет — выгрузи лог."
}

// saveTokens кладёт токены в память и в хранилище учётных данных.
func (c *Client) saveTokens(t tokens) error {
	c.mu.Lock()
	c.tokens = t
	c.mu.Unlock()
	c.log.Redactor.Add(t.AccessToken, t.RefreshToken)
	return c.secrets.PutJSON(keyringName, t)
}

func (c *Client) forgetTokens() {
	c.mu.Lock()
	c.tokens = tokens{}
	c.me = nil
	c.mu.Unlock()
	c.secrets.Delete(keyringName)
}

// Logout забывает вход по кнопке в панели.
func (c *Client) Logout() {
	c.forgetTokens()
	c.log.Info("вход в Spotify сброшен")
}

// randomString даёт строку из символов, разрешённых для code_verifier
// (буквы, цифры, дефис, точка, подчёркивание, тильда).
func randomString(n int) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(buf), nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
