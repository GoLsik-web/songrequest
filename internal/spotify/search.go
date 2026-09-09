package spotify

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"songrequest/internal/match"
)

// SearchTracks ищет треки. Возвращает кандидатов в том виде, в каком их
// понимает пакет match: он ничего не знает про Spotify, и это позволяет
// проверять весь подбор обычными тестами, без сети.
func (c *Client) SearchTracks(ctx context.Context, query string, limit int) ([]match.Candidate, error) {
	return c.searchTracks(ctx, query, limit, c.country())
}

// SearchTracksAnywhere ищет в обход страны аккаунта.
//
// Нужен ровно для одного: объяснить отказ. Если обычный поиск не нашёл
// ничего, а этот находит, значит трек в Spotify есть, но в стране стримера
// не издан, — и зрителю надо сказать именно это, а не «такого трека нет».
// Играть найденное здесь нельзя: Spotify откажет при попытке включить.
func (c *Client) SearchTracksAnywhere(ctx context.Context, query string, limit int) ([]match.Candidate, error) {
	return c.searchTracks(ctx, query, limit, "")
}

// safeSearchLimit — сколько треков заведомо согласится отдать любой Spotify.
//
// 27.08 нашлась причина, из-за которой у тестера не работал поиск текстом
// пятый день: его Spotify отвечает `400 Invalid limit` на любой поиск трека
// с limit больше десяти. Пробы легли ровно так:
//
//	limit=1  → 200      limit=20 → 400 Invalid limit
//	limit=5  → 200      limit=50 → 400 Invalid limit
//	limit=10 → 200
//
// Приложение всюду просило 20 и 50 — значит не находило вообще ничего, ни
// текстом, ни по ссылке на YouTube (её название тоже уходит в этот поиск).
// Документация Spotify по-прежнему обещает 50, и на других аккаунтах 50
// работает, поэтому насмерть занижать всем не будем: см. searchLimitCap.
const safeSearchLimit = 10

// searchLimit — с каким limit идти в этот раз, с учётом уже выясненного
// потолка.
func (c *Client) searchLimit(limit int) int {
	// Больше десяти Spotify не отдаёт никому.
	//
	// В феврале 2026 он опустил предел поиска с пятидесяти до десяти, а
	// значение по умолчанию — с двадцати до пяти. Приложение про это не знало
	// и каждый раз после запуска сначала просило двадцать, получало «400
	// Invalid limit», запоминало потолок и переспрашивало. То есть один
	// заведомо пустой запрос на каждый запуск, да ещё и красная строка в логе
	// на ровном месте.
	//
	// Потолок, выясненный живьём (searchLimitCap), оставлен: он умеет
	// опуститься ещё ниже, если однажды Spotify урежет предел снова.
	if limit <= 0 || limit > safeSearchLimit {
		limit = safeSearchLimit
	}
	c.mu.RLock()
	cap := c.searchLimitCap
	c.mu.RUnlock()
	if cap > 0 && limit > cap {
		return cap
	}
	return limit
}

// capSearchLimit запоминает потолок на весь запуск: платить лишним запросом
// за каждый поиск незачем, отказ один раз объясняет всё.
func (c *Client) capSearchLimit(limit int) {
	c.mu.Lock()
	c.searchLimitCap = limit
	c.mu.Unlock()
}

func (c *Client) searchTracks(ctx context.Context, query string, limit int, market string) ([]match.Candidate, error) {
	limit = c.searchLimit(limit)

	var out struct {
		Tracks struct {
			Items []struct {
				ID         string `json:"id"`
				URI        string `json:"uri"`
				Name       string `json:"name"`
				DurationMs int    `json:"duration_ms"`
				Popularity int    `json:"popularity"`
				// AvailableMarkets Spotify присылает, когда в запросе не задан
				// market. Именно по нему видно, что трек существует, но в
				// стране стримера не играет.
				AvailableMarkets []string `json:"available_markets"`
				Artists          []struct {
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

	// Страну задаём в самом запросе — так же, как это делает заказ по ссылке.
	//
	// Раньше поиск шёл без market и сам отсеивал кандидатов по списку
	// available_markets. Из-за этого два пути расходились: по ссылке Spotify
	// сам подбирал издание, годное для страны аккаунта, и отвечал
	// «is_playable: true», а поиск видел исходное издание, в списке стран
	// которого нужной не было, и выбрасывал верный трек. Со стороны это
	// выглядело как «ссылкой заказать можно, а текстом — нет вообще»: ровно
	// то, на что жаловался тестер.
	path := "/search?type=track&limit=" + strconv.Itoa(limit) + "&q=" + url.QueryEscape(query)
	if market != "" {
		path += "&market=" + url.QueryEscape(market)
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		// «Invalid limit» на верное число означает, что этому аккаунту
		// Spotify отдаёт меньше. Занижаем потолок и переспрашиваем сразу:
		// иначе заказ отменится там, где всё нашлось бы с limit=10.
		// Раньше здесь стоял прыжок к safeSearchLimit. Пока приложение просило
		// двадцать, это работало; теперь оно и так просит десять, то есть
		// ровно safeSearchLimit, и прыгать стало некуда — запасного пути не
		// осталось бы вовсе. Делим пополам: так он есть всегда, сколько бы
		// Spotify ни урезал предел в следующий раз.
		if limit > 1 && isInvalidLimit(err) {
			next := limit / 2
			if next < 1 {
				next = 1
			}
			c.capSearchLimit(next)
			c.log.Warn("Spotify не принимает такой limit — дальше спрашиваем меньше",
				"было", limit, "стало", next)
			return c.searchTracks(ctx, query, next, market)
		}
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
			Markets:    t.AvailableMarkets,
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

// ResolveArtist опознаёт артиста по кривому написанию.
//
// Поиск Spotify по артистам снисходительнее нашего сравнения: «marshmelo» он
// опознаёт как Marshmello, «bilie ailish» — как Billie Eilish. Нам этого
// достаточно, чтобы дальше искать трек внутри одного артиста, а не среди
// всей музыки мира.
func (c *Client) ResolveArtist(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}

	var out struct {
		Artists struct {
			Items []struct {
				Name       string `json:"name"`
				Popularity int    `json:"popularity"`
			} `json:"items"`
		} `json:"artists"`
	}

	path := "/search?type=artist&limit=5&q=" + url.QueryEscape(name)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return "", err
	}

	// Spotify возвращает похожих по своему разумению, и первый не всегда
	// тот: на «marshmelo» приходят и Marshmello, и подражатели с похожими
	// именами. Выбираем сами, по звучанию.
	best, bestScore := "", 0.0
	for _, a := range out.Artists.Items {
		score := match.LooseSimilar(name, a.Name)
		// Популярность решает споры между одинаково похожими: подражатели
		// у Spotify всегда заметно ниже оригинала.
		score += float64(a.Popularity) / 10000
		if score > bestScore {
			best, bestScore = a.Name, score
		}
	}

	// Ниже этого совпадение уже случайное, и сузить поиск таким именем —
	// значит увести его совсем не туда.
	if bestScore < 0.7 {
		c.log.Debug("артист не опознан", "запрос", name, "лучшее", best, "оценка", bestScore)
		return "", nil
	}

	c.log.Info("артист опознан", "как_написали", name, "в_spotify", best)
	return best, nil
}
