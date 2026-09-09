package spotify

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"songrequest/internal/errs"
)

// Плейлисты и альбомы, которые заказывают зрители.
//
// Отдельно от library.go: там плейлисты стримера, из которых он выбирает
// запасной, и оттуда нужны только адреса треков. Здесь наоборот — чужая пачка
// треков, и нужно про каждый всё: название, артист, длительность, обложка.
// Заказ плейлиста — отдельная награда, см. internal/server/playlists.go.
//
// Одна страница, а не постраничный обход всего плейлиста. Причина простая:
// число треков в заказе ограничено настройкой стримера (обычно пять), и качать
// ради них сорок страниц плейлиста на тысячу треков — это выброшенная норма
// запросов Spotify, самое узкое место всего приложения.

// CollectionTrack — трек из плейлиста или альбома.
type CollectionTrack struct {
	ID         string
	URI        string
	Title      string
	Artist     string
	DurationMs int
	CoverURL   string
}

// Collection — плейлист или альбом целиком.
type Collection struct {
	// Title — название, для человека.
	Title string
	// Tracks — треки по порядку, уже обрезанные до нужного числа.
	Tracks []CollectionTrack
	// Total — сколько треков всего. Нужно, чтобы честно сказать «взял первые
	// пять из сорока».
	Total int
}

// collectionLimit — сколько треков спрашивать у Spotify за раз.
//
// Просим чуть больше, чем нужно: в плейлистах попадаются локальные файлы и
// снятые с продажи треки, и без запаса пачка из пяти могла бы приехать
// втроём. Верхний предел Spotify — сто.
func collectionLimit(want int) int {
	limit := want + 10
	if limit > 100 {
		limit = 100
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

// spotifyTrackJSON — то, что Spotify рассказывает о треке. Один вид на плейлист
// и на альбом: в альбоме нет обложки у самого трека, её берём у альбома.
type spotifyTrackJSON struct {
	ID      string `json:"id"`
	URI     string `json:"uri"`
	Name    string `json:"name"`
	IsLocal bool   `json:"is_local"`
	Type    string `json:"type"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"artists"`
	DurationMs int `json:"duration_ms"`
	Album      struct {
		Images []struct {
			URL string `json:"url"`
		} `json:"images"`
	} `json:"album"`
}

func (t spotifyTrackJSON) toTrack(fallbackCover string) (CollectionTrack, bool) {
	// Локальные файлы играть нельзя: их нет на серверах Spotify. Эпизоды
	// подкастов тоже мимо — у них другой вид, и в очередь музыки им не место.
	if t.URI == "" || t.IsLocal || strings.TrimSpace(t.Name) == "" {
		return CollectionTrack{}, false
	}
	if t.Type != "" && t.Type != "track" {
		return CollectionTrack{}, false
	}

	artist := ""
	if len(t.Artists) > 0 {
		artist = t.Artists[0].Name
	}
	cover := fallbackCover
	if len(t.Album.Images) > 0 {
		cover = t.Album.Images[0].URL
	}
	return CollectionTrack{
		ID: t.ID, URI: t.URI, Title: t.Name, Artist: artist,
		DurationMs: t.DurationMs, CoverURL: cover,
	}, true
}

// PlaylistCollection читает чужой плейлист: название и первые треки.
func (c *Client) PlaylistCollection(ctx context.Context, id string, want int) (Collection, error) {
	id = strings.TrimPrefix(id, "spotify:playlist:")
	if !safeSpotifyID(id) {
		return Collection{}, errs.New(errs.SpotifyNothing, "Непонятная ссылка на плейлист Spotify.")
	}

	var out struct {
		Name   string `json:"name"`
		Tracks struct {
			Total int `json:"total"`
			Items []struct {
				Track *spotifyTrackJSON `json:"track"`
			} `json:"items"`
		} `json:"tracks"`
	}

	// Одним запросом: название плейлиста и первая страница треков сразу.
	path := fmt.Sprintf("/playlists/%s?fields=name,tracks(total,items(track(id,uri,name,is_local,type,duration_ms,artists(name),album(images))))",
		url.PathEscape(id))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return Collection{}, err
	}

	col := Collection{Title: strings.TrimSpace(out.Name), Total: out.Tracks.Total}
	for _, item := range out.Tracks.Items {
		if len(col.Tracks) >= want {
			break
		}
		if item.Track == nil {
			continue
		}
		if track, ok := item.Track.toTrack(""); ok {
			col.Tracks = append(col.Tracks, track)
		}
	}
	if len(col.Tracks) == 0 {
		return Collection{}, errs.New(errs.SpotifyNothing,
			"В этом плейлисте Spotify нечего играть.")
	}
	return col, nil
}

// AlbumCollection читает альбом: название и первые треки.
//
// У треков внутри альбома своей обложки нет — Spotify не повторяет её у
// каждого. Берём обложку самого альбома: без неё в панели и в кадре у всей
// пачки было бы пустое место.
func (c *Client) AlbumCollection(ctx context.Context, id string, want int) (Collection, error) {
	id = strings.TrimPrefix(id, "spotify:album:")
	if !safeSpotifyID(id) {
		return Collection{}, errs.New(errs.SpotifyNothing, "Непонятная ссылка на альбом Spotify.")
	}

	var out struct {
		Name   string `json:"name"`
		Images []struct {
			URL string `json:"url"`
		} `json:"images"`
		Tracks struct {
			Total int                `json:"total"`
			Items []spotifyTrackJSON `json:"items"`
		} `json:"tracks"`
	}

	path := fmt.Sprintf("/albums/%s?limit=%d", url.PathEscape(id), collectionLimit(want))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return Collection{}, err
	}

	cover := ""
	if len(out.Images) > 0 {
		cover = out.Images[0].URL
	}

	col := Collection{Title: strings.TrimSpace(out.Name), Total: out.Tracks.Total}
	for _, item := range out.Tracks.Items {
		if len(col.Tracks) >= want {
			break
		}
		if track, ok := item.toTrack(cover); ok {
			col.Tracks = append(col.Tracks, track)
		}
	}
	if len(col.Tracks) == 0 {
		return Collection{}, errs.New(errs.SpotifyNothing, "В этом альбоме Spotify нечего играть.")
	}
	return col, nil
}

// safeSpotifyID проверяет опознавательный номер.
//
// Он приходит из сообщения зрителя и уходит прямо в адрес запроса. У Spotify
// это всегда двадцать два знака латиницы и цифр; всё остальное — попытка
// увести запрос не туда.
func safeSpotifyID(s string) bool {
	if len(s) != 22 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
