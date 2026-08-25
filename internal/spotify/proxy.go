package spotify

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"songrequest/internal/errs"
)

// Прокси только для Spotify.
//
// Полноценного «обхода» не бывает: если сеть не пускает к Spotify, трафику
// всё равно нужно выйти наружу через что-то. Из кода сетевую блокировку не
// отменить, и обещать этого нельзя.
//
// Зато можно не гонять через посредника всё подряд. Системный VPN уводит на
// чужой маршрут вообще весь компьютер: игру, голосовой чат, OBS и загрузку
// стрима на Twitch — то есть ровно то, чему лишние сто миллисекунд вредят
// больше всего. А приложению посредник нужен для одного адреса.
//
// Поэтому прокси здесь свой и только для Spotify. Остальное — Twitch, чат,
// донаты, выгрузка стрима — идёт напрямую и ничего не теряет.
//
// Важно, о чём легко забыть: сама программа Spotify тоже ходит в сеть, и
// музыку играет она. Один прокси в приложении её не спасёт — тот же адрес
// нужно вписать и в настройках Spotify: Настройки → Прокси.

// proxyTimeout больше обычного — через посредника всё медленнее, — но обязан
// быть заметно меньше срока подбора трека (25 секунд, см. resolveTimeout).
// Иначе один зависший запрос физически не может уложиться в этот срок, и
// каждый заказ заканчивается советом «проверь интернет», хотя интернет
// ни при чём.
const proxyTimeout = 12 * time.Second

// SetProxy направляет запросы к Spotify через посредника.
//
// Пустая строка возвращает прямое соединение. Возвращает разобранный адрес
// без пароля — его показывают в панели и пишут в лог.
func (c *Client) SetProxy(raw string) (string, error) {
	raw = strings.TrimSpace(raw)

	if raw == "" {
		c.setHTTP(&http.Client{Timeout: 15 * time.Second})
		c.mu.Lock()
		c.proxyLabel = ""
		c.mu.Unlock()
		c.log.Info("Spotify: прямое соединение")
		return "", nil
	}

	u, err := ParseProxy(raw)
	if err != nil {
		return "", err
	}

	// Пароль от прокси — такой же секрет, как ключ доступа: он не должен
	// попасть ни в лог, ни в выгрузку.
	if pass, ok := u.User.Password(); ok && pass != "" {
		c.log.Redactor.Add(pass)
	}

	c.setHTTP(&http.Client{
		Timeout: proxyTimeout,
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(u),
			TLSHandshakeTimeout: 10 * time.Second,
		},
	})
	label := Hide(u)
	c.mu.Lock()
	c.proxyLabel = label
	c.mu.Unlock()

	c.log.Info("Spotify ходит через прокси", "адрес", label)
	return label, nil
}

// ProxyLabel — через кого сейчас ходим, без пароля. Пусто — напрямую.
//
// Отдельный метод, а не поле в карточке: карточка пересобирается с нуля на
// каждом обновлении, и всё, что записано в неё мимо этой сборки, тут же
// затирается. Один раз уже затёрлось.
func (c *Client) ProxyLabel() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.proxyLabel
}

// ParseProxy разбирает адрес посредника и объясняет ошибку по-человечески.
//
// Люди вставляют адрес как придётся: «1.2.3.4:1080», «socks5://…», с логином
// и паролем и без. Отвечать на это словом «ошибка» нельзя — человек не
// поймёт, что именно он написал не так.
func ParseProxy(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errProxy("Адрес прокси пустой.")
	}

	// Разбираем руками, а не url.Parse. В паролях от прокси попадается всё:
	// собака, решётка, проценты, кириллица. Готовый разборщик на таком
	// спотыкается и объявляет ошибкой нормальный адрес.
	scheme := "socks5"
	rest := raw
	if i := strings.Index(raw, "://"); i >= 0 {
		scheme = strings.ToLower(raw[:i])
		rest = raw[i+3:]
	}

	switch scheme {
	case "socks5", "socks5h", "http", "https":
	default:
		return nil, errProxy("Прокси вида «" + scheme + "» приложение не умеет. " +
			"Подойдёт socks5, http или https.")
	}

	// Собаку ищем последнюю: она может встретиться и внутри пароля.
	var user *url.Userinfo
	hostPort := rest
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		creds := rest[:i]
		hostPort = rest[i+1:]
		if j := strings.Index(creds, ":"); j >= 0 {
			user = url.UserPassword(creds[:j], creds[j+1:])
		} else if creds != "" {
			user = url.User(creds)
		}
	}

	hostPort = strings.Trim(hostPort, "/")
	host, port, ok := splitHostPort(hostPort)
	switch {
	case host == "":
		return nil, errProxy("В адресе прокси нет самого адреса.")
	case !ok || port == "":
		return nil, errProxy("В адресе прокси не указан порт. " +
			"Он пишется через двоеточие в конце: адрес:1080")
	}

	return &url.URL{Scheme: scheme, User: user, Host: hostPort}, nil
}

// splitHostPort делит «адрес:порт», не спотыкаясь об IPv6 в скобках.
func splitHostPort(s string) (host, port string, ok bool) {
	if s == "" {
		return "", "", false
	}
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 {
			host = s[1:i]
			if rest := s[i+1:]; strings.HasPrefix(rest, ":") {
				return host, rest[1:], true
			}
			return host, "", false
		}
		return "", "", false
	}
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}

// Hide собирает адрес для показа: всё узнаваемо, пароля нет.
//
// Собираем строку сами, а не через url.URL.String(): тот кодирует всё
// подряд процентами, и вместо «user:…@host» получалось «user%3A%E2%80%A6@host» —
// прочитать это невозможно.
func Hide(u *url.URL) string {
	if u == nil {
		return ""
	}
	creds := ""
	if name := u.User.Username(); name != "" {
		creds = name
		if _, hasPass := u.User.Password(); hasPass {
			creds += ":…"
		}
		creds += "@"
	}
	return u.Scheme + "://" + creds + u.Host
}

// errProxy — ошибка настройки прокси. Отдельный код, чтобы стример мог
// назвать его по телефону, не пересказывая текст.
func errProxy(message string) error {
	return errs.New(errs.SpotifyProxy, message)
}
