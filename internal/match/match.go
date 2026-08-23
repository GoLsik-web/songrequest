package match

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Searcher — то, что умеет искать треки. Интерфейсом, чтобы пакет не знал
// про Spotify и целиком проверялся тестами без сети.
type Searcher interface {
	SearchTracks(ctx context.Context, query string, limit int) ([]Candidate, error)
}

// Options — пороги и веса.
type Options struct {
	// Accept — выше этого берём молча.
	Accept float64
	// Maybe — между Maybe и Accept берём, но помечаем как неточное совпадение.
	Maybe   float64
	Weights Weights
	// WantMs — длительность из ссылки, если она известна.
	WantMs int
}

// DefaultOptions — значения по умолчанию.
func DefaultOptions() Options {
	return Options{Accept: 0.80, Maybe: 0.55, Weights: DefaultWeights()}
}

// Result — чем закончился поиск.
type Result struct {
	Found     bool
	Uncertain bool // нашли, но неуверенно: в панели помечаем
	Track     Candidate
	Score     Score
	// Attempts — какие запросы делались. Нужно, чтобы по логу было понятно,
	// почему поиск ничего не нашёл.
	Attempts []string
	// Considered — сколько разных треков попало в общий пул.
	Considered int
}

// Explain — короткое объяснение выбора для лога и панели.
func (r Result) Explain() string {
	if !r.Found {
		return fmt.Sprintf("ничего подходящего среди %d кандидатов", r.Considered)
	}
	return fmt.Sprintf("%.2f · %s", r.Score.Total, r.Score.Why)
}

// Find ищет заказанный трек.
//
// Стратегия — несколько попыток по убыванию строгости. Результаты всех
// попыток складываются в общий пул и оцениваются вместе: строгий запрос может
// не найти ничего из-за одной опечатки, а свободный — вернуть верный трек
// пятым в списке.
func Find(ctx context.Context, s Searcher, req Request, opts Options) (Result, error) {
	pool := map[string]Candidate{}
	var result Result

	for _, q := range queries(req) {
		if q == "" {
			continue
		}
		result.Attempts = append(result.Attempts, q)

		found, err := s.SearchTracks(ctx, q, 20)
		if err != nil {
			// Одна неудачная попытка не повод бросать поиск: остальные могут
			// сработать. Ошибку вернём, только если не нашлось совсем ничего.
			if len(pool) == 0 && len(result.Attempts) == len(queries(req)) {
				return result, err
			}
			continue
		}
		for _, c := range found {
			pool[c.ID] = c
		}

		// Останавливаемся, как только набрали уверенного кандидата: остальные
		// запросы только потратят лимит Spotify.
		if best, score := pick(req, pool, opts); best.ID != "" && score.Total >= opts.Accept {
			result.Found = true
			result.Track = best
			result.Score = score
			result.Considered = len(pool)
			return result, nil
		}
	}

	result.Considered = len(pool)
	best, score := pick(req, pool, opts)
	if best.ID == "" || score.Total < opts.Maybe {
		return result, nil
	}

	result.Found = true
	result.Track = best
	result.Score = score
	result.Uncertain = score.Total < opts.Accept
	return result, nil
}

// pick выбирает лучшего кандидата из пула.
func pick(req Request, pool map[string]Candidate, opts Options) (Candidate, Score) {
	// Порядок обхода карты в Go случайный, поэтому при равных оценках выбор
	// был бы разным от запуска к запуску. Сортируем по идентификатору.
	ids := make([]string, 0, len(pool))
	for id := range pool {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var best Candidate
	var bestScore Score

	for _, id := range ids {
		c := pool[id]

		// Примеряем все прочтения запроса: прямое, с переставленными
		// артистом и названием, и переписанное другим алфавитом. Берём
		// лучшее — какое из них верное, заранее неизвестно.
		score := Rate(req, c, opts.WantMs, opts.Weights)
		for _, variant := range readings(req) {
			if alt := Rate(variant, c, opts.WantMs, opts.Weights); alt.Total > score.Total {
				score = alt
			}
		}

		if score.Total > bestScore.Total {
			best, bestScore = c, score
		}
	}
	return best, bestScore
}

// readings — все разумные прочтения одного и того же заказа.
func readings(req Request) []Request {
	out := []Request{}
	if req.HasParts() {
		out = append(out, req.Flip())
	}
	if t := req.Translit(); t.Title != req.Title || t.Artist != req.Artist {
		out = append(out, t)
		if t.HasParts() {
			out = append(out, t.Flip())
		}
	}
	return out
}

// queries строит попытки от самой строгой к самой свободной.
func queries(req Request) []string {
	var out []string
	add := func(q string) {
		q = strings.TrimSpace(q)
		if q == "" {
			return
		}
		for _, existing := range out {
			if existing == q {
				return
			}
		}
		out = append(out, q)
	}

	title := StripEditionSuffix(req.Title)
	artist := req.Artist

	if artist != "" && title != "" {
		// 1. Структурированный поиск по полям — самый точный.
		add(fmt.Sprintf("track:%s artist:%s", quoted(title), quoted(artist)))

		// 2. То же, но без доп. артистов: Spotify часто не находит трек,
		// когда в поле артиста перечислены все участники.
		if len(req.Feats) > 0 {
			add(fmt.Sprintf("track:%s artist:%s", quoted(title), quoted(primaryArtist(artist))))
		}

		// 3. Свободный запрос: поле track подводит, когда в названии есть
		// скобки или пометки.
		add(artist + " " + title)

		// 4. Обратный порядок — вдруг разделитель прочитан наоборот.
		add(title + " " + artist)
	}

	// 5. Только название: артист мог быть распознан неверно или отсутствовать.
	add(title)

	// 6. Транслитерация: русские артисты в Spotify часто записаны латиницей.
	if artist != "" && title != "" {
		if t := Transliterate(artist + " " + title); t != "" {
			add(t)
		}
	} else if t := Transliterate(title); t != "" {
		add(t)
	}

	return out
}

// quoted оборачивает значение в кавычки, если в нём есть пробелы.
func quoted(s string) string {
	s = strings.ReplaceAll(s, `"`, "")
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

// primaryArtist оставляет только первого исполнителя.
func primaryArtist(s string) string {
	if parts := splitArtists(s); len(parts) > 0 {
		return parts[0]
	}
	return s
}
