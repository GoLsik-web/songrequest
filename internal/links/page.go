package links

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Ссылки на остальные музыкальные сервисы.
//
// Появилось 11.10 по просьбе владельца «проверь ссылки от всего, от чего
// можно». Сыграть трек из Apple Music, SoundCloud или Deezer приложение не
// может, но может узнать, что это за трек, и найти его в Spotify: каждая такая
// страница для превью в соцсетях пишет в метаданные название и артиста
// (og:title и соседние). Этого хватает для поиска.
//
// Ходим только на сайты из списка. Ссылку пишет зритель, а читаем её с
// компьютера стримера — любой другой адрес мог бы оказаться чем угодно в его
// домашней сети. Перенаправления тоже проверяем по списку.

// pageHosts — какие сайты читаем и как их называть в логе.
var pageHosts = map[string]string{
	"music.apple.com":   "Apple Music",
	"soundcloud.com":    "SoundCloud",
	"on.soundcloud.com": "SoundCloud",
	"deezer.com":        "Deezer",
	"deezer.page.link":  "Deezer",
	"link.deezer.com":   "Deezer",
	"zvuk.com":          "Звук",
	"shazam.com":        "Shazam",
	"music.mts.ru":      "МТС Музыка",
	"song.link":         "song.link",
	"album.link":        "song.link",
	"odesli.co":         "song.link",
	"tidal.com":         "Tidal",
	"listen.tidal.com":  "Tidal",
}

// pageService — что это за сервис, если сайт из списка.
func pageService(host string) (string, bool) {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	if name, ok := pageHosts[host]; ok {
		return name, true
	}
	for h, name := range pageHosts {
		if strings.HasSuffix(host, "."+h) {
			return name, true
		}
	}
	return "", false
}

var (
	metaTag      = regexp.MustCompile(`(?is)<meta\s+[^>]*>`)
	metaAttr     = regexp.MustCompile(`(?is)(property|name|content)\s*=\s*("[^"]*"|'[^']*')`)
	pageTitleTag = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

// PageInfo — что удалось узнать со страницы.
type PageInfo struct {
	Service string
	Title   string // og:title или <title>
	Artist  string // music:musician, если сайт его пишет
	Extra   string // og:description — у некоторых сайтов артист только там
	// DurationMs — длительность, если сервис её сказал (Apple, Deezer).
	DurationMs int
}

// Query — текст для поиска в Spotify, без хвостов вида «— Apple Music».
func (p PageInfo) Query() string {
	q := cleanPageTitle(p.Title)
	if p.Artist != "" && !strings.Contains(strings.ToLower(q), strings.ToLower(p.Artist)) {
		q = p.Artist + " - " + q
	}
	return q
}

// pageJunk — хвосты и приставки, которые сайты дописывают к названию.
var pageJunk = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\s*песня\s+`),
	regexp.MustCompile(`(?i)\s+в\s+apple.music.*$`),
	regexp.MustCompile(`(?i)\s*[|—–-]\s*(apple music|deezer|soundcloud|shazam|звук|tidal|song\.link|odesli|мтс музыка|zvuk).*$`),
	regexp.MustCompile(`(?i)^\s*stream\s+`),
	regexp.MustCompile(`(?i)\s+(listen online for free on soundcloud|on soundcloud).*$`),
	regexp.MustCompile(`(?i)\s*[—–-]\s*(песня|song|single|сингл|трек|track)\s+(by|исполнителя|от)?\s*`),
	regexp.MustCompile(`(?i)\s+by\s+`),
	regexp.MustCompile(`(?i)^\s*(слушать|listen to)\s+`),
}

func cleanPageTitle(t string) string {
	for _, re := range pageJunk {
		t = re.ReplaceAllString(t, " ")
	}
	return strings.Join(strings.Fields(t), " ")
}

// ReadPage читает метаданные страницы музыкального сервиса.
func ReadPage(ctx context.Context, link Link) (PageInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	client := &http.Client{
		// Перенаправления — только на сайты из того же списка: короткие
		// ссылки (on.soundcloud.com, deezer.page.link) ведут на основной сайт,
		// а не куда-то ещё.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return errors.New("слишком много перенаправлений")
			}
			if req.URL.Scheme != "https" {
				return errors.New("перенаправление не на https")
			}
			if _, ok := pageService(req.URL.Hostname()); !ok {
				return errors.New("перенаправление на чужой сайт")
			}
			return nil
		},
	}
	if info, ok := lookupExact(ctx, client, link); ok {
		return info, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link.URL, nil)
	if err != nil {
		return PageInfo{}, err
	}
	// Как браузер: многие сайты без этого отдают пустую заглушку.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0 Safari/537.36")
	req.Header.Set("Accept-Language", "ru,en;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return PageInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return PageInfo{}, errors.New("сайт ответил " + resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return PageInfo{}, err
	}

	info := PageInfo{Service: link.Service}
	meta := map[string]string{}
	for _, tag := range metaTag.FindAllString(string(body), -1) {
		var key, content string
		for _, a := range metaAttr.FindAllStringSubmatch(tag, -1) {
			v := html.UnescapeString(strings.Trim(a[2], `"'`))
			switch strings.ToLower(a[1]) {
			case "property", "name":
				key = strings.ToLower(v)
			case "content":
				content = v
			}
		}
		if key != "" && content != "" {
			if _, seen := meta[key]; !seen {
				meta[key] = content
			}
		}
	}
	info.Title = firstFilled(meta["og:title"], meta["twitter:title"])
	if info.Title == "" {
		if m := pageTitleTag.FindSubmatch(body); m != nil {
			info.Title = html.UnescapeString(strings.TrimSpace(string(m[1])))
		}
	}
	// music:musician у Apple — ссылка на страницу артиста, не имя: берём
	// только если это не адрес.
	if a := meta["music:musician_description"]; a != "" {
		info.Artist = a
	} else if a := meta["music:musician"]; a != "" && !strings.HasPrefix(a, "http") {
		info.Artist = a
	}
	info.Extra = firstFilled(meta["og:description"], meta["description"])
	if strings.TrimSpace(info.Title) == "" {
		return PageInfo{}, errors.New("на странице нет названия трека")
	}
	return info, nil
}

// pageLink — ссылка на сайт из списка, если это она.
func pageLink(u *url.URL, raw string) (Link, bool) {
	if u.Scheme != "https" && u.Scheme != "http" {
		return Link{}, false
	}
	name, ok := pageService(u.Hostname())
	if !ok {
		return Link{}, false
	}
	u.Scheme = "https"
	return Link{Kind: Page, URL: u.String(), Service: name}, true
}

// Точные справочники. Apple и Deezer отдают по номеру трека артиста,
// название и длительность в открытом виде — это надёжнее заголовка страницы
// («Песня «Группа крови» (Кино) в Apple Music») и даёт длительность, самый
// сильный признак против каверов.

var (
	appleTrackID  = regexp.MustCompile(`[?&]i=(\d+)|/song/(?:[^/]+/)?(\d+)`)
	deezerTrackID = regexp.MustCompile(`/track/(\d+)`)
)

// lookupExact спрашивает справочник сервиса. false — справочника нет или он
// не ответил, тогда читаем страницу.
func lookupExact(ctx context.Context, client *http.Client, link Link) (PageInfo, bool) {
	switch link.Service {
	case "Apple Music":
		m := appleTrackID.FindStringSubmatch(link.URL)
		if m == nil {
			return PageInfo{}, false
		}
		var out struct {
			Results []struct {
				ArtistName      string `json:"artistName"`
				TrackName       string `json:"trackName"`
				TrackTimeMillis int    `json:"trackTimeMillis"`
			} `json:"results"`
		}
		if !getJSON(ctx, client, "https://itunes.apple.com/lookup?id="+firstFilled(m[1], m[2]), &out) || len(out.Results) == 0 {
			return PageInfo{}, false
		}
		r := out.Results[0]
		return PageInfo{Service: link.Service, Title: r.TrackName, Artist: r.ArtistName, DurationMs: r.TrackTimeMillis}, r.TrackName != ""

	case "Deezer":
		// Короткие ссылки (deezer.page.link) сначала раскрываем.
		target := link.URL
		if !deezerTrackID.MatchString(target) {
			if resp, err := client.Get(target); err == nil {
				resp.Body.Close()
				target = resp.Request.URL.String()
			}
		}
		m := deezerTrackID.FindStringSubmatch(target)
		if m == nil {
			return PageInfo{}, false
		}
		var out struct {
			Title    string `json:"title"`
			Duration int    `json:"duration"`
			Artist   struct {
				Name string `json:"name"`
			} `json:"artist"`
		}
		if !getJSON(ctx, client, "https://api.deezer.com/track/"+m[1], &out) {
			return PageInfo{}, false
		}
		return PageInfo{Service: link.Service, Title: out.Title, Artist: out.Artist.Name, DurationMs: out.Duration * 1000}, out.Title != ""
	}
	return PageInfo{}, false
}

func getJSON(ctx context.Context, client *http.Client, addr string, out any) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out) == nil
}
