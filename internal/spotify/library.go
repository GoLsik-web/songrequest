package spotify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"songrequest/internal/errs"
)

// Playlist — плейлист стримера для выпадающего списка в настройках.
type Playlist struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	URI    string `json:"uri"`
	Tracks int    `json:"tracks"`
	Owner  string `json:"owner"`
}

// HasPlaylistAccess сообщает, выдал ли стример право читать список плейлистов.
// Права запрашиваются при входе, поэтому у того, кто подключился на прошлой
// версии приложения, их нет — ему нужно нажать «Подключить заново».
func (c *Client) HasPlaylistAccess() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return strings.Contains(c.tokens.Scope, "playlist-read-private")
}

// Playlists отдаёт плейлисты стримера, чтобы он выбрал запасной из списка,
// а не искал и вставлял ссылку руками.
func (c *Client) Playlists(ctx context.Context) ([]Playlist, error) {
	if !c.Connected() {
		return nil, errs.New(errs.SpotifyAuthExpired,
			"Spotify не подключён. Нажми «Подключить Spotify».")
	}
	if !c.HasPlaylistAccess() {
		return nil, errs.New(errs.SpotifyNoScope,
			"Чтобы приложение видело твои плейлисты, нужно один раз переподключить Spotify — нажми «Подключить заново».")
	}

	var out []Playlist

	// Плейлистов бывает несколько сотен, Spotify отдаёт их страницами по 50.
	for offset := 0; offset < 500; offset += 50 {
		var page struct {
			Items []struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				URI   string `json:"uri"`
				Owner struct {
					DisplayName string `json:"display_name"`
				} `json:"owner"`
				Tracks struct {
					Total int `json:"total"`
				} `json:"tracks"`
			} `json:"items"`
			Next string `json:"next"`
		}

		path := fmt.Sprintf("/me/playlists?limit=50&offset=%d", offset)
		if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
			if errors.Is(err, errNoContent) {
				break
			}
			return nil, err
		}

		for _, it := range page.Items {
			out = append(out, Playlist{
				ID:     it.ID,
				Name:   it.Name,
				URI:    it.URI,
				Tracks: it.Tracks.Total,
				Owner:  it.Owner.DisplayName,
			})
		}
		if page.Next == "" {
			break
		}
	}

	c.log.Debug("прочитал список плейлистов", "штук", len(out))
	return out, nil
}

// PlaylistTrackURIs берёт первые треки плейлиста — ими дозаполняем
// воспроизведение, когда вернуть исходный источник не вышло.
func (c *Client) PlaylistTrackURIs(ctx context.Context, playlistID string, limit int) ([]string, error) {
	id := strings.TrimPrefix(playlistID, "spotify:playlist:")

	var page struct {
		Items []struct {
			Track *struct {
				URI     string `json:"uri"`
				IsLocal bool   `json:"is_local"`
			} `json:"track"`
		} `json:"items"`
	}

	path := fmt.Sprintf("/playlists/%s/tracks?limit=%d&fields=items(track(uri,is_local))",
		url.PathEscape(id), limit)
	if err := c.do(ctx, http.MethodGet, path, nil, &page); err != nil {
		return nil, err
	}

	uris := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		// Локальные файлы и удалённые треки играть нельзя — пропускаем молча.
		if it.Track == nil || it.Track.URI == "" || it.Track.IsLocal {
			continue
		}
		uris = append(uris, it.Track.URI)
	}
	return uris, nil
}

// ArtistTopTrackURIs — популярные треки артиста. Это наша замена «радио»:
// эндпоинт рекомендаций Spotify закрыт для новых приложений, а этот работает.
func (c *Client) ArtistTopTrackURIs(ctx context.Context, artistID string, limit int) ([]string, error) {
	if artistID == "" {
		return nil, errs.New(errs.SpotifyNothing, "Не знаю, от какого артиста включать музыку.")
	}

	var out struct {
		Tracks []struct {
			URI string `json:"uri"`
		} `json:"tracks"`
	}

	path := "/artists/" + url.PathEscape(artistID) + "/top-tracks"
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}

	uris := make([]string, 0, len(out.Tracks))
	for i, t := range out.Tracks {
		if i >= limit {
			break
		}
		uris = append(uris, t.URI)
	}
	return uris, nil
}

// QueueTrack ставит трек в очередь самого Spotify — так после возвращённого
// трека музыка продолжится, а не оборвётся тишиной.
func (c *Client) QueueTrack(ctx context.Context, trackURI, deviceID string) error {
	path := "/me/player/queue?uri=" + url.QueryEscape(trackURI)
	if deviceID != "" {
		path += "&device_id=" + url.QueryEscape(deviceID)
	}
	return c.do(ctx, http.MethodPost, path, nil, nil)
}

// PlayTracks включает список треков подряд, начиная с первого.
func (c *Client) PlayTracks(ctx context.Context, uris []string, deviceID string) error {
	if len(uris) == 0 {
		return errs.New(errs.SpotifyNothing, "Включать нечего.")
	}
	return c.do(ctx, http.MethodPut, "/me/player/play"+deviceQuery(deviceID),
		&playBody{URIs: uris}, nil)
}
