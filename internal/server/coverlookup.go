package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// Запасной поиск обложки — без Spotify.
//
// Обложку своей музыки раньше давал только Spotify. Но к нему из России
// ходят через обход, и когда обход лежит (а у владельца 21.10 лежали все
// серверы ключа разом), плашка в кадре оставалась без обложки на каждом
// треке. Название и артиста приложение и так знает из панели Windows, а
// по ним обложку отдают открытые каталоги iTunes и Deezer: без ключей,
// без входа и напрямую, без всякого обхода.

// lookupClient — отдельный, с коротким ожиданием: обложка нужна сейчас,
// пока трек играет, а не через полминуты.
var lookupClient = &http.Client{Timeout: 5 * time.Second}

// coverLookup — через переменную, чтобы проверки не ходили в настоящий
// интернет: им подставляется заглушка.
var coverLookup = lookupCover

// lookupCover ищет обложку по артисту и названию. Пусто — не нашлось.
func lookupCover(ctx context.Context, artist, title string) string {
	if strings.TrimSpace(title) == "" {
		return ""
	}
	if u := lookupITunes(ctx, artist, title); u != "" {
		return u
	}
	return lookupDeezer(ctx, artist, title)
}

// squash — строка без регистра, пробелов и знаков: «Quest Pistols Show» и
// «QUEST PISTOLS» должны узнавать друг друга.
func squash(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sameArtist — похож ли найденный артист на нашего. Каталог на запрос
// «артист трек» охотно вернёт кавер другой группы, и тогда в кадре была
// бы чужая обложка — хуже, чем никакой.
func sameArtist(want, got string) bool {
	a, b := squash(want), squash(got)
	if a == "" || b == "" {
		return a == b
	}
	return strings.Contains(a, b) || strings.Contains(b, a)
}

func getJSON(ctx context.Context, link string, v any) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return false
	}
	resp, err := lookupClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v) == nil
}

func lookupITunes(ctx context.Context, artist, title string) string {
	var res struct {
		Results []struct {
			ArtistName string `json:"artistName"`
			ArtworkURL string `json:"artworkUrl100"`
		} `json:"results"`
	}
	q := url.Values{"term": {artist + " " + title}, "entity": {"song"}, "limit": {"8"}}
	if !getJSON(ctx, "https://itunes.apple.com/search?"+q.Encode(), &res) {
		return ""
	}
	for _, r := range res.Results {
		if r.ArtworkURL != "" && sameArtist(artist, r.ArtistName) {
			// Каталог отдаёт значок 100×100; та же картинка покрупнее
			// лежит по тому же адресу с другим размером в имени.
			return strings.Replace(r.ArtworkURL, "100x100bb", "600x600bb", 1)
		}
	}
	return ""
}

func lookupDeezer(ctx context.Context, artist, title string) string {
	var res struct {
		Data []struct {
			Artist struct {
				Name string `json:"name"`
			} `json:"artist"`
			Album struct {
				CoverXL string `json:"cover_xl"`
			} `json:"album"`
		} `json:"data"`
	}
	q := url.Values{"q": {artist + " " + title}, "limit": {"8"}}
	if !getJSON(ctx, "https://api.deezer.com/search?"+q.Encode(), &res) {
		return ""
	}
	for _, r := range res.Data {
		if r.Album.CoverXL != "" && sameArtist(artist, r.Artist.Name) {
			return r.Album.CoverXL
		}
	}
	return ""
}
