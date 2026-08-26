package server

import (
	"context"
	"strings"

	"songrequest/internal/app"
	"songrequest/internal/links"
	"songrequest/internal/match"
	"songrequest/internal/queue"
	"songrequest/internal/twitch"
)

// linkResult — что удалось понять из ссылки.
type linkResult struct {
	// Track — готовый трек Spotify: подбирать нечего, зритель указал точно.
	Track *match.Candidate
	// Query — текст для обычного поиска, восстановленный по ссылке.
	Query string
	// WantMs — длительность из ссылки. Самый сильный сигнал против каверов,
	// ускоренных версий и часовых лупов.
	WantMs int
	// YouTube — играть напрямую с YouTube, минуя Spotify.
	YouTube *queue.Item
}

// fromLink разбирает ссылку в заказе.
//
// Порядок важен: ссылка Spotify даёт трек сразу, остальные — текст для
// поиска. Если поиск по нему ничего не даст, ссылка на YouTube или VK
// останется запасным вариантом, и заказ всё равно сыграет.
func (s *Server) fromLink(ctx context.Context, text string) (linkResult, bool) {
	link, ok := links.Find(text)
	if !ok {
		return linkResult{}, false
	}

	switch link.Kind {
	case links.Spotify:
		track, err := s.spotify.Track(ctx, link.ID)
		if err != nil {
			s.log.Warn("не смог взять трек по ссылке Spotify", "ссылка", link.URL, "ошибка", err)
			return linkResult{}, false
		}
		s.log.Info("трек взят по ссылке Spotify",
			"трек", firstArtist(track.Artists)+" — "+track.Title)
		return linkResult{Track: &track}, true

	case links.Yandex:
		// Официального API у Яндекс.Музыки нет: читаем мета-теги страницы.
		// Не вышло — не беда, поищем по остальному тексту заказа.
		meta, err := s.yandex.Read(ctx, link.URL)
		if err != nil {
			s.log.Info("не прочитал страницу Яндекс.Музыки", "ссылка", link.URL, "ошибка", err)
			return linkResult{}, false
		}
		s.log.Info("трек со страницы Яндекс.Музыки",
			"артист", meta.Artist, "название", meta.Title)
		return linkResult{Query: meta.Query()}, true

	case links.YouTube, links.VK:
		if s.youtube == nil || !s.ytTools.Ready() {
			return linkResult{}, false
		}
		track, err := s.youtube.Lookup(ctx, link.URL)
		if err != nil {
			s.log.Info("не разобрал ссылку", "ссылка", link.URL, "ошибка", err)
			return linkResult{}, false
		}
		if track.IsLive {
			s.log.Info("по ссылке стрим, а не трек", "ссылка", link.URL)
			return linkResult{}, false
		}

		s.log.Info("метаданные по ссылке",
			"откуда", link.Kind, "артист", track.Artist, "название", track.Title,
			"длительность_мс", track.DurationMs)

		// Сначала попробуем найти то же самое в Spotify: играть оттуда
		// лучше — не нужен отдельный проигрыватель и звук не расходится.
		return linkResult{
			Query:  track.Artist + " - " + track.Title,
			WantMs: track.DurationMs,
			YouTube: &queue.Item{
				Provider:   "youtube",
				TrackID:    track.ID,
				URI:        track.URL,
				Title:      track.Title,
				Artist:     track.Artist,
				DurationMs: track.DurationMs,
				CoverURL:   track.CoverURL,
			},
		}, true
	}

	return linkResult{}, false
}

// acceptTrack ставит готовый трек Spotify в очередь.
//
// Общий путь для всех источников: ссылка, память подбора и обычный поиск
// заканчиваются здесь, чтобы заказ выглядел и проверялся одинаково.
func (s *Server) acceptTrack(ctx context.Context, r twitch.Redemption,
	track match.Candidate, uncertain bool, note string) {

	state := app.MatchFound
	if uncertain {
		state = app.MatchUncertain
	}

	artist := firstArtist(track.Artists)

	s.state.SetOrderMatch(r.ID, app.OrderMatch{
		State:    state,
		TrackID:  track.ID,
		URI:      track.URI,
		Title:    track.Title,
		Artist:   artist,
		CoverURL: track.CoverURL,
		Duration: track.DurationMs,
		Note:     note,
	})

	s.enqueue(ctx, queue.Item{
		Source:         queue.SourcePoints,
		Requester:      r.UserName,
		RequesterLogin: strings.ToLower(r.UserLogin),
		RawRequest:     r.UserInput,
		Provider:       "spotify",
		TrackID:        track.ID,
		URI:            track.URI,
		Title:          track.Title,
		Artist:         artist,
		DurationMs:     track.DurationMs,
		CoverURL:       track.CoverURL,
		Uncertain:      uncertain,
		RedemptionID:   r.ID,
		RewardID:       r.RewardID,
	})
}

// acceptYouTube ставит в очередь то, что играется мимо Spotify.
func (s *Server) acceptYouTube(ctx context.Context, r twitch.Redemption, item queue.Item) {
	s.state.SetOrderMatch(r.ID, app.OrderMatch{
		State:    app.MatchFound,
		TrackID:  item.TrackID,
		URI:      item.URI,
		Title:    item.Title,
		Artist:   item.Artist,
		CoverURL: item.CoverURL,
		Duration: item.DurationMs,
		Note:     "Играем с YouTube — в Spotify такого нет",
	})

	item.Source = queue.SourcePoints
	item.Requester = r.UserName
	item.RequesterLogin = strings.ToLower(r.UserLogin)
	item.RawRequest = r.UserInput
	item.RedemptionID = r.ID
	item.RewardID = r.RewardID
	s.enqueue(ctx, item)
}
