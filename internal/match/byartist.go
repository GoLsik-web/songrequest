package match

import (
	"context"
	"fmt"
)

// Поиск через артиста.
//
// Это последний заход, когда обычные запросы не дали ничего годного. Смысл
// такой: имя артиста зритель обычно узнаёт правильнее, чем название трека, —
// его он видел на обложке, слышал сто раз и пишет хоть и на слух, но узнаваемо.
//
// «маршмело - элон» прямым запросом Spotify не находит: «marshmelo elon» для
// него набор букв. Но поиск по артистам у Spotify снисходительнее нашего, и
// «marshmelo» он опознаёт как Marshmello. А дальше задача становится
// простой: найти «элон» среди треков одного артиста, а не среди всей музыки
// мира. Там уже хватает сравнения на слух — Alone находится.
//
// Так же лечится и обратный случай: зритель верно написал название, но
// исковеркал артиста настолько, что общий запрос уходил в пустоту.

// ArtistResolver — тот, кто умеет опознать артиста по кривому написанию.
// Реализуется поверх поиска Spotify по артистам; для подбора это
// необязательная способность, и без неё всё продолжает работать.
type ArtistResolver interface {
	// ResolveArtist возвращает каноническое имя артиста или пустую строку,
	// если ничего похожего нет.
	ResolveArtist(ctx context.Context, name string) (string, error)
}

// byArtist доискивает трек, опознав артиста.
//
// Возвращает найденных кандидатов и запросы, которые для этого делались, —
// чтобы в логе было видно, что мы пробовали.
func byArtist(ctx context.Context, s Searcher, req Request) ([]Candidate, []string) {
	resolver, ok := s.(ArtistResolver)
	if !ok || req.Artist == "" || req.Title == "" {
		return nil, nil
	}

	// Пробуем то, как написал зритель, и то же в латинице: у Spotify индекс
	// артистов латинский, и «маршмело» он не поймёт, а «marshmelo» поймёт.
	var tried []string
	for _, name := range []string{req.Artist, Transliterate(req.Artist)} {
		if name == "" || contains(tried, name) {
			continue
		}
		tried = append(tried, name)

		canonical, err := resolver.ResolveArtist(ctx, name)
		if err != nil || canonical == "" {
			continue
		}

		// Имя артиста теперь точное — сужаем поиск до него одного.
		query := fmt.Sprintf("artist:%s %s", quoted(canonical), req.Title)
		found, err := s.SearchTracks(ctx, query, 50)
		if err != nil {
			continue
		}
		if len(found) > 0 {
			return found, []string{query}
		}

		// Название зритель мог исковеркать сильнее, чем имя. Тогда берём
		// каталог артиста целиком и выбираем по звучанию уже сами.
		wide := fmt.Sprintf("artist:%s", quoted(canonical))
		found, err = s.SearchTracks(ctx, wide, 50)
		if err == nil && len(found) > 0 {
			return found, []string{query, wide}
		}
	}
	return nil, tried
}

func contains(list []string, want string) bool {
	for _, x := range list {
		if x == want {
			return true
		}
	}
	return false
}

// artistOnly — зритель написал только имя артиста.
//
// 11.10: заказ «slawa marlow» нашёлся как «Носовой Платон — Слава Марлов» —
// чужая песня, у которой имя артиста стоит в названии. Зритель же хотел
// самого SLAVA MARLOW, просто не сказал какой трек. Если весь заказ на слух
// совпадает с именем артиста, которого знает Spotify, играем его самый
// популярный трек. Стоит два запроса и только тогда, когда обычный подбор
// уверенного ответа не дал.
func artistOnly(ctx context.Context, s Searcher, req Request) (Candidate, string, bool) {
	resolver, ok := s.(ArtistResolver)
	if !ok || req.Artist != "" || req.Title == "" {
		return Candidate{}, "", false
	}
	for _, name := range []string{req.Title, Transliterate(req.Title)} {
		if name == "" {
			continue
		}
		canonical, err := resolver.ResolveArtist(ctx, name)
		if err != nil || canonical == "" {
			continue
		}
		// Запрос должен быть именем целиком, а не «имя + что-то ещё»: иначе
		// «кино группа крови» превратилось бы в любой популярный трек Кино.
		if LooseSimilar(Normalize(name), Normalize(canonical)) < 0.8 {
			continue
		}
		query := fmt.Sprintf("artist:%s", quoted(canonical))
		found, err := s.SearchTracks(ctx, query, 10)
		if err != nil {
			continue
		}
		var best Candidate
		for _, c := range found {
			mine := false
			for _, a := range c.Artists {
				if Normalize(a) == Normalize(canonical) {
					mine = true
				}
			}
			if mine && c.Popularity > best.Popularity || mine && best.ID == "" {
				best = c
			}
		}
		if best.ID != "" {
			return best, query, true
		}
	}
	return Candidate{}, "", false
}
