package youtube

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"songrequest/internal/errs"
)

// OEmbed узнаёт название и автора ролика через открытый сервис YouTube.
//
// Запасной путь для того случая, когда yt-dlp упёрся в проверку «подтвердите,
// что вы не бот». 11.10 так было на компьютере, где весь интернет шёл через
// VPN: ссылка на ролик падала целиком, хотя тот же трек лежал в Spotify и
// сыграть его можно было оттуда. oEmbed — обычный открытый адрес YouTube для
// встраивания роликов на сайты; проверки он не требует, но и длительности не
// знает. Для поиска в Spotify хватает названия и автора.
func OEmbed(ctx context.Context, link string) (*Track, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://www.youtube.com/oembed?format=json&url="+url.QueryEscape(link), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errs.Wrap(errs.YouTubeBadResponse, "YouTube не ответил на вопрос о ролике.", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errs.Newf(errs.YouTubeNotFound,
			"YouTube не знает такого ролика (ответ %d).", resp.StatusCode)
	}
	var out struct {
		Title     string `json:"title"`
		Author    string `json:"author_name"`
		Thumbnail string `json:"thumbnail_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, errs.Wrap(errs.YouTubeBadResponse, "YouTube ответил непонятно.", err)
	}
	// У авто-каналов YouTube Music имя вида «SLAVA MARLOW - Topic».
	author := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(out.Author), "- Topic"))
	return &Track{
		Title:    strings.TrimSpace(out.Title),
		Artist:   author,
		URL:      link,
		CoverURL: out.Thumbnail,
	}, nil
}
