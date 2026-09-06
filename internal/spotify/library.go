package spotify

import (
	"context"
	"encoding/json"
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
// версии приложения, их нет — ему нужно ещё раз нажать «Подключить Spotify».
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
			"Чтобы приложение видело твои плейлисты, нужно один раз переподключить Spotify — нажми «Подключить Spotify» ещё раз.")
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

		// Тестер видит «0 треков» у непустых плейлистов, и по разобранному
		// ответу этого не понять. Пишем в отладку то, что реально пришло.
		for _, it := range page.Items {
			c.log.Debug("плейлист от Spotify",
				"название", it.Name, "треков", it.Tracks.Total,
				"владелец", it.Owner.DisplayName, "id", it.ID)
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
	// Spotify иногда отдаёт в списке ноль треков у непустых плейлистов.
	// Спорить с ним бесполезно — спрашиваем число там, где оно точно есть:
	// у самого плейлиста. Один лишний запрос на плейлист, и только на те,
	// что показались пустыми.
	c.fillEmptyCounts(ctx, out)

	total := 0
	empty := 0
	for _, p := range out {
		total += p.Tracks
		if p.Tracks == 0 {
			empty++
		}
	}
	c.log.Info("плейлисты прочитаны",
		"всего", len(out), "суммарно_треков", total, "пустых", empty,
		"страна_аккаунта", c.country())

	return out, nil
}

// country — страна вошедшего аккаунта, если она уже известна.
func (c *Client) country() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.me == nil {
		return ""
	}
	return c.me.Country
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
	return c.play(ctx, &playBody{URIs: uris}, deviceID)
}

// fillEmptyCounts дозапрашивает число треков у плейлистов, где список отдал ноль.
//
// Ограничение по числу запросов не от жадности: у иного стримера полторы
// сотни плейлистов, и опрашивать каждый — значит заставить его ждать минуту
// ради подписи в выпадающем списке.
func (c *Client) fillEmptyCounts(ctx context.Context, list []Playlist) {
	const maxAsks = 25

	asked := 0
	for i := range list {
		if list[i].Tracks > 0 {
			continue
		}
		if asked >= maxAsks {
			return
		}
		asked++

		// Без `fields`: сокращённый ответ — лишний повод для Spotify отдать
		// не то, а разбирать потом придётся по чужому логу, вслепую.
		// Сырой ответ забираем целиком, чтобы в логе было видно, что пришло.
		var raw json.RawMessage
		path := "/playlists/" + url.PathEscape(list[i].ID) + "/tracks?limit=1"
		if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
			c.log.Warn("не переспросил число треков",
				"плейлист", list[i].Name, "id", list[i].ID, "ошибка", err)
			continue
		}

		var page struct {
			Total int `json:"total"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			c.log.Warn("ответ про число треков не разобрался",
				"плейлист", list[i].Name, "ответ", head(raw), "ошибка", err)
			continue
		}

		if page.Total <= 0 {
			// Тестер видел «0 треков» у плейлиста, который сам же и слушал.
			// Если Spotify упорствует, пусть в логе останется его ответ:
			// иначе следующий разбор снова упрётся в догадки.
			c.log.Warn("Spotify второй раз говорит, что плейлист пуст",
				"плейлист", list[i].Name, "id", list[i].ID, "ответ", head(raw))
			continue
		}

		c.log.Info("число треков уточнено",
			"плейлист", list[i].Name, "было", 0, "стало", page.Total)
		list[i].Tracks = page.Total
	}
}

// head — начало ответа для лога. Целиком класть незачем: в списке треков
// плейлиста первый же трек занимает пару килобайт, а нам нужна только форма
// ответа.
func head(data []byte) string {
	const limit = 400
	if len(data) <= limit {
		return string(data)
	}
	return string(data[:limit]) + "…"
}
