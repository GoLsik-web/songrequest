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

	switch {
	case youtubeID.MatchString(raw):
		id := youtubeID.FindStringSubmatch(raw)[1]
		return Link{Kind: YouTube, ID: id, URL: "https://www.youtube.com/watch?v=" + id}, true

	case yandexTrack.MatchString(raw):
		m := yandexTrack.FindStringSubmatch(raw)
		id := firstFilled(m[2], m[3])
		return Link{Kind: Yandex, ID: id, URL: clean(raw)}, true

	case vkAny.MatchString(raw):
		return Link{Kind: VK, URL: clean(raw)}, true
	}

	return Link{}, false
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
