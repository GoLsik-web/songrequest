// Package links разбирает то, что кинул зритель.
//
// Ссылка точнее любого поиска: зритель уже указал конкретный трек, и гадать
// по названию не нужно. Поэтому ссылка всегда важнее текста, а поиск по
// словам остаётся запасным вариантом.
package links

import (
	"net/url"
	"regexp"
	"strings"
)

// Kind — откуда ссылка.
type Kind string

const (
	// None — ссылки нет, это просто текст.
	None Kind = ""
	// Spotify — можно взять трек напрямую по идентификатору, без поиска.
	Spotify Kind = "spotify"
	// YouTube — метаданные достанет yt-dlp.
	YouTube Kind = "youtube"
	// Yandex — официального API нет, придётся читать саму страницу.
	Yandex Kind = "yandex"
	// VK — метаданные достанет yt-dlp, он умеет и VK.
	VK Kind = "vk"
)

// Link — разобранная ссылка.
type Link struct {
	Kind Kind
	// URL — очищенный адрес: без меток рекламных кампаний и прочего мусора,
	// который ломает и сравнение, и запросы.
	URL string
	// ID — идентификатор трека, если он есть в самой ссылке.
	ID string
}

// Найденное — не ссылка, а адрес внутри текста: зрители пишут
// «врубай вот это <ссылка> пж», и вырезать её надо аккуратно.
var urlPattern = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s<>"']+`)

var (
	spotifyTrack = regexp.MustCompile(`(?i)(?:open\.spotify\.com/(?:intl-[a-z]{2}/)?track/|spotify:track:)([a-zA-Z0-9]{22})`)
	youtubeID    = regexp.MustCompile(`(?i)(?:youtube\.com/(?:watch\?(?:.*&)?v=|shorts/|embed/|live/)|youtu\.be/|music\.youtube\.com/watch\?(?:.*&)?v=)([a-zA-Z0-9_\-]{11})`)
	yandexTrack  = regexp.MustCompile(`(?i)music\.yandex\.[a-z]+/(?:album/(\d+)/track/(\d+)|track/(\d+))`)
	vkAny        = regexp.MustCompile(`(?i)(?:vk\.com|vk\.ru|vkvideo\.ru)/`)
)

// Find ищет в тексте ссылку и определяет, что это.
//
// Возвращает false, если ссылки нет или она не из тех, что мы умеем.
func Find(text string) (Link, bool) {
	// Спотифаевский uri пишется без http, поэтому его ищем прямо в тексте.
	if m := spotifyTrack.FindStringSubmatch(text); m != nil {
		return Link{Kind: Spotify, ID: m[1], URL: "https://open.spotify.com/track/" + m[1]}, true
	}

	raw := urlPattern.FindString(text)
	if raw == "" {
		return Link{}, false
	}
	if strings.HasPrefix(strings.ToLower(raw), "www.") {
		raw = "https://" + raw
	}
	// Зрители часто копируют ссылку вместе с точкой или скобкой в конце.
	raw = strings.TrimRight(raw, ".,;:!?)]}»\"'")

	// Дальше решаем по настоящему имени хоста, а не по подстроке в адресе.
	//
	// Раньше здесь искалось «music.yandex.» и «vk.com/» где угодно в строке —
	// в том числе в пути. Зритель за баллы писал заказ вида
	// «https://192.168.1.1/music.yandex.ru/track/1», приложение объявляло это
	// Яндекс.Музыкой и уходило читать страницу с адреса, который он выбрал:
	// с компьютера стримера, из его домашней сети. С «vk.com/» в пути тот же
	// адрес уезжал в yt-dlp, который ходит с куками из браузера стримера.
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return Link{}, false
	}
	host := strings.ToLower(u.Hostname())

	switch {
	case hostIn(host, "youtube.com", "youtu.be", "music.youtube.com"):
		m := youtubeID.FindStringSubmatch(raw)
		if m == nil {
			return Link{}, false
		}
		return Link{Kind: YouTube, ID: m[1], URL: "https://www.youtube.com/watch?v=" + m[1]}, true

	case host == "music.yandex.ru" || host == "music.yandex.com" ||
		host == "music.yandex.by" || host == "music.yandex.kz" || host == "music.yandex.uz":
		m := yandexTrack.FindStringSubmatch(raw)
		if m == nil {
			return Link{}, false
		}
		id := firstFilled(m[2], m[3])
		return Link{Kind: Yandex, ID: id, URL: clean(raw)}, true

	case hostIn(host, "vk.com", "vk.ru", "vkvideo.ru"):
		return Link{Kind: VK, URL: clean(raw)}, true
	}

	return Link{}, false
}

// hostIn сверяет имя хоста со списком: сам домен или что-то под ним.
//
// Суффиксное сравнение обязательно с точкой: без неё «злойyoutube.com» и
// «notvk.com» прошли бы как свои.
func hostIn(host string, domains ...string) bool {
	for _, d := range domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// Strip убирает из текста любые адреса — остаётся то, что зритель написал
// словами. Искать в Spotify по адресу бесполезно, чей бы он ни был.
func Strip(text string) string {
	out := urlPattern.ReplaceAllString(text, " ")
	out = spotifyTrack.ReplaceAllString(out, " ")
	return strings.Join(strings.Fields(out), " ")
}

// clean снимает с адреса рекламные метки и якоря: они не часть ссылки на
// трек, а сравнивать и запрашивать с ними хуже.
func clean(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	for key := range q {
		low := strings.ToLower(key)
		if strings.HasPrefix(low, "utm_") || low == "si" || low == "feature" ||
			low == "from" || low == "reqid" || low == "context" {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	u.Fragment = ""
	return u.String()
}

func firstFilled(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
