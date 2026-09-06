package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Пробник поиска.
//
// У тестера Spotify отвечает `400 Invalid limit` на каждый поиск трека, при
// том что поиск артиста, `/me`, плеер и список плейлистов у того же аккаунта
// работают. Ни в подборе, ни в коде причины нет: она снаружи — в стране
// аккаунта, в провайдере или в чём-то, что стоит между приложением и Spotify.
// По логу этого не различить, поэтому спрашиваем Spotify одно и то же
// несколькими способами подряд и записываем, что он ответил на каждый.
//
// Ответ пробника — не для стримера, а для разбора: он жмёт кнопку и присылает
// лог.

// Probe — одна проба: как спросили и что ответили.
type Probe struct {
	// Way — способ человеческими словами: «limit=20», «с market=IN».
	Way string `json:"way"`
	// Path — адрес запроса целиком, как он ушёл в сеть.
	Path string `json:"path"`
	// HTTP — код ответа. 0 означает, что ответа не было вовсе.
	HTTP int `json:"http"`
	// Found — сколько нашлось. Для плейлиста — сколько в нём треков.
	Found int `json:"found"`
	// Body — ответ Spotify. У отказов он короткий и в нём вся суть.
	Body string `json:"body"`
	// Error — сбой связи, а не отказ Spotify.
	Error string `json:"error"`
}

// OK — проба прошла.
func (p Probe) OK() bool { return p.HTTP >= 200 && p.HTTP < 300 }

// ProbeSearch перебирает способы спросить Spotify и пишет каждый ответ в лог.
//
// query — что искать, playlistID — чьё содержимое пробовать читать. Пустой
// плейлист пропускается: пробы по нему просто не делаются.
func (c *Client) ProbeSearch(ctx context.Context, query, playlistID string) []Probe {
	if strings.TrimSpace(query) == "" {
		query = "into you"
	}
	market := c.country()

	c.log.Info("проверка поиска началась",
		"запрос", query, "страна_аккаунта", market, "плейлист", playlistID)

	var probes []Probe
	add := func(way, path string) {
		p := c.probe(ctx, way, path)
		probes = append(probes, p)
	}

	q := url.QueryEscape(query)

	// 1. Тот же запрос с разным limit. Если Spotify ругается на «Invalid
	// limit», должно быть видно, какое значение он всё-таки принимает.
	for _, limit := range []int{1, 5, 10, 20, 50} {
		add("тип track, limit="+strconv.Itoa(limit),
			"/search?type=track&limit="+strconv.Itoa(limit)+"&q="+q)
	}

	// 2. Со страной аккаунта и без неё: заказ по ссылке её задаёт, а поиск
	// раньше не задавал — и работало ровно одно из двух.
	if market != "" {
		add("тип track, limit=5, market="+market,
			"/search?type=track&limit=5&market="+url.QueryEscape(market)+"&q="+q)
	}

	// 3. Порядок полей наоборот. Если дело в нём, значит адрес разбирает не
	// Spotify, а что-то по дороге.
	add("тот же запрос, поля в обратном порядке",
		"/search?q="+q+"&limit=5&type=track")

	// 4. Адрес, собранный по правилам, а не склейкой строк.
	values := url.Values{"q": {query}, "type": {"track"}, "limit": {"5"}}
	add("адрес собран через url.Values", "/search?"+values.Encode())

	// 5. Два типа сразу — вдруг отказ вызывает именно одиночный track.
	add("тип track,artist, limit=5",
		"/search?type=track,artist&limit=5&q="+q)

	// 6. Поиск артиста — тот, что у тестера работает. Опора для сравнения:
	// если и он откажет, дело не в поиске треков, а во всём поиске сразу.
	add("тип artist, limit=5 (этот способ работал)",
		"/search?type=artist&limit=5&q="+q)

	// 7. Содержимое плейлиста: у тестера на него 403, отчего «в плейлистах
	// 0 треков» и не работает дозаполнение тишины после очереди.
	if id := strings.TrimPrefix(playlistID, "spotify:playlist:"); id != "" {
		id = url.PathEscape(id)
		add("плейлист: сам плейлист (этот способ работал)", "/playlists/"+id)
		add("плейлист: треки, limit=1", "/playlists/"+id+"/tracks?limit=1")
		if market != "" {
			add("плейлист: треки, limit=1, market="+market,
				"/playlists/"+id+"/tracks?limit=1&market="+url.QueryEscape(market))
		}
		add("плейлист: треки, только число", "/playlists/"+id+"/tracks?fields=total")
		add("плейлист: треки, как их берёт приложение",
			"/playlists/"+id+"/tracks?limit=1&fields=items(track(uri,is_local))")
	}

	ok := 0
	for _, p := range probes {
		if p.OK() {
			ok++
		}
	}
	c.log.Info("проверка поиска закончена",
		"проб", len(probes), "удачных", ok, "страна_аккаунта", market)

	return probes
}

// probe делает один запрос и записывает всё, что о нём известно.
//
// Мимо do(): повторы, перевод отказов в человеческие слова и обновление
// ключа здесь только мешают — нужен сырой ответ Spotify как он есть.
func (c *Client) probe(ctx context.Context, way, path string) Probe {
	p := Probe{Way: way, Path: path}

	token, err := c.token(ctx)
	if err != nil {
		p.Error = err.Error()
		c.log.Warn("проба поиска", "способ", way, "путь", path, "ошибка", err)
		return p
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+path, nil)
	if err != nil {
		p.Error = err.Error()
		return p
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client().Do(req)
	if err != nil {
		p.Error = err.Error()
		c.log.Warn("проба поиска", "способ", way, "путь", path, "ошибка", err)
		return p
	}
	defer resp.Body.Close()

	data, err := readBody(resp)
	if err != nil {
		p.Error = err.Error()
		c.log.Warn("проба поиска", "способ", way, "путь", path, "ошибка", err)
		return p
	}

	p.HTTP = resp.StatusCode
	p.Found = countFound(data)
	p.Body = shorten(string(data), 600)

	c.log.Info("проба поиска",
		"способ", way, "путь", path, "код_http", p.HTTP,
		"нашлось", p.Found, "ответ", p.Body)
	return p
}

// countFound считает, сколько всего пришло: треков, артистов или треков в
// плейлисте — смотря о чём спрашивали.
func countFound(data []byte) int {
	var out struct {
		Tracks struct {
			Items []json.RawMessage `json:"items"`
			Total int               `json:"total"`
		} `json:"tracks"`
		Artists struct {
			Items []json.RawMessage `json:"items"`
		} `json:"artists"`
		Items []json.RawMessage `json:"items"`
		Total int               `json:"total"`
	}
	if json.Unmarshal(data, &out) != nil {
		return 0
	}
	switch {
	case len(out.Tracks.Items) > 0:
		return len(out.Tracks.Items)
	case len(out.Artists.Items) > 0:
		return len(out.Artists.Items)
	case len(out.Items) > 0:
		return len(out.Items)
	case out.Tracks.Total > 0:
		// Так отвечает сам плейлист: треков внутри он не показывает, но их
		// число называет.
		return out.Tracks.Total
	default:
		return out.Total
	}
}

// shorten обрезает длинный ответ: удачный поиск — это десятки килобайт, и
// класть их целиком в лог и в панель незачем.
func shorten(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	// Режем по буквам, а не по байтам: в ответе бывает кириллица, и половина
	// буквы превратит лог в мусор.
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + fmt.Sprintf("… (всего %d знаков)", len(r))
}
