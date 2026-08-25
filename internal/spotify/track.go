package spotify

import (
	"context"
	"net/http"
	"net/url"

	"songrequest/internal/errs"
	"songrequest/internal/match"
)

// Track берёт трек напрямую по идентификатору.
//
// Ссылка точнее любого поиска: зритель уже указал конкретную запись, и
// подбирать по названию тут нечего — ошибиться можно только испортив.
func (c *Client) Track(ctx context.Context, id string) (match.Candidate, error) {
	var t struct {
		ID         string `json:"id"`
		URI        string `json:"uri"`
		Name       string `json:"name"`
		DurationMs int    `json:"duration_ms"`
		Popularity int    `json:"popularity"`
		// IsPlayable приходит только когда в запросе указан рынок. Именно он
		// отвечает на вопрос «а заиграет ли это у стримера».
		IsPlayable *bool `json:"is_playable"`
		Artists    []struct {
			Name string `json:"name"`
		} `json:"artists"`
		Album struct {
			Name      string `json:"name"`
			AlbumType string `json:"album_type"`
			Images    []struct {
				URL string `json:"url"`
			} `json:"images"`
		} `json:"album"`
	}

	// Рынок обязателен. Без него Spotify отвечает про трек «вообще», и заказ
	// по ссылке на трек, не изданный в стране аккаунта, спокойно вставал в
	// очередь: баллы списаны, а падало оно только в момент включения — и с
	// текстом про устройство, который к делу не относится.
	path := "/tracks/" + url.PathEscape(id)
	if market := c.country(); market != "" {
		path += "?market=" + url.QueryEscape(market)
	}

	if err := c.do(ctx, http.MethodGet, path, nil, &t); err != nil {
		return match.Candidate{}, err
	}
	if t.ID == "" {
		return match.Candidate{}, errs.New(errs.SpotifyBadResponse,
			"Spotify не нашёл трек по этой ссылке.")
	}
	if t.IsPlayable != nil && !*t.IsPlayable {
		return match.Candidate{}, errs.New(errs.SpotifyForeignTrack,
			"Этот трек не издан в стране аккаунта Spotify у стримера — включить его не выйдет.")
	}

	cand := match.Candidate{
		ID:         t.ID,
		URI:        t.URI,
		Title:      t.Name,
		Album:      t.Album.Name,
		AlbumType:  t.Album.AlbumType,
		DurationMs: t.DurationMs,
		Popularity: t.Popularity,
	}
	for _, a := range t.Artists {
		cand.Artists = append(cand.Artists, a.Name)
	}
	if n := len(t.Album.Images); n > 0 {
		cand.CoverURL = t.Album.Images[n-1].URL
	}
	return cand, nil
}
