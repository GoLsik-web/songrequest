package links

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"songrequest/internal/errs"
)

// Официального публичного API у Яндекс.Музыки нет, поэтому артиста и
// название приходится доставать из самой страницы — из тех же мета-тегов,
// по которым мессенджеры рисуют превью ссылки.
//
// Это хрупко по своей природе: вёрстку могут поменять когда угодно. Поэтому
// здесь всё обёрнуто так, чтобы неудача была обычным делом — заказ просто
// уйдёт в обычный поиск по тексту, — и разбор покрыт тестами на настоящей
// разметке, чтобы поломку заметить сразу.

// Meta — то, что удалось вытащить со страницы трека.
type Meta struct {
	Artist string
	Title  string
}

// Query собирает строку для поиска в Spotify.
func (m Meta) Query() string {
	switch {
	case m.Artist != "" && m.Title != "":
		return m.Artist + " - " + m.Title
	case m.Title != "":
		return m.Title
	default:
		return ""
	}
}

// YandexReader читает страницу трека.
type YandexReader struct {
	// HTTP можно подменить в тестах, чтобы проверять разбор на подставном
	// сайте, а не ходить в настоящий Яндекс.
	HTTP *http.Client
}

// NewYandexReader создаёт читалку.
func NewYandexReader() *YandexReader {
	return &YandexReader{HTTP: &http.Client{Timeout: 15 * time.Second}}
}

var (
	ogTitle       = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]+content=["']([^"']*)["']`)
	ogTitleRev    = regexp.MustCompile(`(?is)<meta[^>]+content=["']([^"']*)["'][^>]+property=["']og:title["']`)
	ogDescription = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:description["'][^>]+content=["']([^"']*)["']`)
	ogDescRev     = regexp.MustCompile(`(?is)<meta[^>]+content=["']([^"']*)["'][^>]+property=["']og:description["']`)
	titleTag      = regexp.MustCompile(`(?is)<title[^>]*>([^<]*)</title>`)
)

// Read достаёт артиста и название со страницы трека.
func (y *YandexReader) Read(ctx context.Context, pageURL string) (Meta, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return Meta{}, errs.Wrap(errs.YandexRead, "Не получилось открыть ссылку Яндекс.Музыки.", err)
	}
	// Без обычного заголовка браузера Яндекс отдаёт страницу-заглушку без
	// мета-тегов, и разбирать становится нечего.
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	req.Header.Set("Accept-Language", "ru,en;q=0.9")

	resp, err := y.HTTP.Do(req)
	if err != nil {
		return Meta{}, errs.Wrap(errs.YandexRead, "Яндекс.Музыка не отвечает.", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Meta{}, errs.New(errs.YandexRead,
			fmt.Sprintf("Яндекс.Музыка ответила ошибкой (%d).", resp.StatusCode))
	}

	// Страница большая, а нужное лежит в самом начале, в head.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return Meta{}, errs.Wrap(errs.YandexRead, "Страница Яндекс.Музыки не дочиталась.", err)
	}

	meta := ParseYandexPage(string(body))
	if meta.Title == "" {
		return Meta{}, errs.New(errs.YandexRead,
			"На странице Яндекс.Музыки не нашлось названия трека.")
	}
	return meta, nil
}

// ParseYandexPage вытаскивает артиста и название из разметки.
//
// Отдельной функцией, чтобы разбор проверялся тестами на настоящей вёрстке,
// а не только живым запросом.
func ParseYandexPage(html string) Meta {
	title := unescape(firstMatch(html, ogTitle, ogTitleRev))
	desc := unescape(firstMatch(html, ogDescription, ogDescRev))

	if title == "" {
		// Запасной вариант: заголовок вкладки. Он выглядит как
		// «Слушать Название — Артист на Яндекс Музыке».
		title = unescape(firstMatch(html, titleTag))
	}
	if title == "" {
		return Meta{}
	}

	return splitTitle(title, desc)
}

// splitTitle разбирает заголовок страницы.
//
// Яндекс пишет его несколькими способами, и все встречаются:
//
//	«Артист — Название»
//	«Название — Артист. Слушать онлайн на Яндекс Музыке»
//	«Слушать Название — Артист на Яндекс Музыке»
func splitTitle(title, desc string) Meta {
	title = trimYandexTail(title)

	// Описание обычно начинается с имени артиста — по нему и понимаем,
	// с какой стороны от тире он стоит.
	artistHint := strings.TrimSpace(strings.SplitN(desc, "•", 2)[0])
	artistHint = strings.TrimSpace(strings.SplitN(artistHint, "·", 2)[0])
	artistHint = trimYandexTail(artistHint)

	left, right, ok := splitDash(title)
	if !ok {
		return Meta{Title: title, Artist: artistHint}
	}

	switch {
	case artistHint != "" && strings.EqualFold(artistHint, right):
		return Meta{Artist: right, Title: left}
	case artistHint != "" && strings.EqualFold(artistHint, left):
		return Meta{Artist: left, Title: right}
	default:
		// Подсказки нет — берём привычный порядок «Артист — Название».
		return Meta{Artist: left, Title: right}
	}
}

var yandexTails = []string{
	"слушать онлайн на яндекс музыке", "на яндекс музыке", "яндекс музыка",
	"слушать онлайн", "listen online on yandex music", "yandex music",
}

// trimYandexTail убирает приписки самого сервиса.
func trimYandexTail(s string) string {
	s = strings.TrimSpace(s)
	low := strings.ToLower(s)
	for _, tail := range yandexTails {
		if i := strings.Index(low, tail); i > 0 {
			s = strings.TrimSpace(s[:i])
			low = strings.ToLower(s)
		}
	}
	s = strings.TrimPrefix(s, "Слушать ")
	return strings.Trim(strings.TrimSpace(s), ".,—-–|")
}

// splitDash делит по тире с пробелами — внутри названий дефис встречается
// часто, а тире с пробелами почти всегда разделитель.
func splitDash(s string) (left, right string, ok bool) {
	for _, sep := range []string{" — ", " – ", " - "} {
		if i := strings.Index(s, sep); i > 0 {
			left = strings.TrimSpace(s[:i])
			right = strings.TrimSpace(s[i+len(sep):])
			if left != "" && right != "" {
				return left, right, true
			}
		}
	}
	return "", "", false
}

func firstMatch(s string, patterns ...*regexp.Regexp) string {
	for _, re := range patterns {
		if m := re.FindStringSubmatch(s); m != nil && strings.TrimSpace(m[1]) != "" {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

var htmlEntities = strings.NewReplacer(
	"&amp;", "&", "&quot;", `"`, "&#39;", "'", "&apos;", "'",
	"&lt;", "<", "&gt;", ">", "&nbsp;", " ", "&mdash;", "—", "&ndash;", "–",
)

func unescape(s string) string {
	return strings.TrimSpace(htmlEntities.Replace(s))
}
