package spotify

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"songrequest/internal/match"
)

// SearchTracks ищет треки. Возвращает кандидатов в том виде, в каком их
// понимает пакет match: он ничего не знает про Spotify, и это позволяет
// проверять весь подбор обычными тестами, без сети.
func (c *Client) SearchTracks(ctx context.Context, query string, limit int) ([]match.Candidate, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var out struct {
		Tracks struct {
			Items []struct {
				ID         string `json:"id"`
				URI        string `json:"uri"`
				Name       string `json:"name"`
				DurationMs int    `json:"duration_ms"`
				Popularity int    `json:"popularity"`
				Artists    []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Album struct {
					Name        string `json:"name"`
					AlbumType   string `json:"album_type"`
					ReleaseDate string `json:"release_date"`
					Images      []struct {
						URL string `json:"url"`
					} `json:"images"`
				} `json:"album"`
			} `json:"items"`
		} `json:"tracks"`
	}

	path := "/search?type=track&limit=" + strconv.Itoa(limit) + "&q=" + url.QueryEscape(query)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}

	candidates := make([]match.Candidate, 0, len(out.Tracks.Items))
	for _, t := range out.Tracks.Items {
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
		// Берём самую маленькую обложку: она уходит в панель и в виджет,
		// а разглядывать её в 640 пикселей никто не будет.
		if n := len(t.Album.Images); n > 0 {
			cand.CoverURL = t.Album.Images[n-1].URL
		}
		candidates = append(candidates, cand)
	}

	c.log.Debug("поиск в Spotify", "запрос", query, "нашлось", len(candidates))
	return candidates, nil
}
