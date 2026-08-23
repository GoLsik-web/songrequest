package server

import (
	"context"
	"time"

	"songrequest/internal/app"
	"songrequest/internal/config"
	"songrequest/internal/errs"
	"songrequest/internal/match"
	"songrequest/internal/twitch"
)

// resolveTimeout — сколько ждём подбор трека. Пять запросов к Spotify с
// повторами укладываются с запасом, а зритель не должен ждать минуту.
const resolveTimeout = 25 * time.Second

// resolveOrder ищет заказанный трек в Spotify.
//
// Очереди ещё нет, поэтому найденный трек пока просто показывается в панели.
// Но весь путь — разбор текста, поиск, оценка, кэш — уже настоящий, и именно
// он определит, что заиграет, когда появится очередь.
func (s *Server) resolveOrder(ctx context.Context, r twitch.Redemption) {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	req := match.Parse(r.UserInput)
	key := match.Key(req)

	if req.Title == "" {
		s.log.Info("заказ без текста", "зритель", r.UserLogin)
		s.state.SetOrderMatch(r.ID, app.OrderMatch{
			State: app.MatchFailed,
			Note:  "Зритель не написал, что заказывает",
		})
		return
	}

	// Сначала кэш: один и тот же трек заказывают десятками за стрим.
	if hit, ok, err := s.matchCache.Get(ctx, key); err != nil {
		s.log.Warn("не прочитал кэш подбора", "ошибка", err)
	} else if ok {
		s.log.Info("трек взят из памяти",
			"заказ", r.UserInput, "трек", hit.Artist+" — "+hit.Title, "ручное", hit.Manual)
		s.state.SetOrderMatch(r.ID, app.OrderMatch{
			State:   app.MatchFound,
			TrackID: hit.TrackID,
			Title:   hit.Title,
			Artist:  hit.Artist,
			Note:    memoryNote(hit.Manual),
		})
		return
	}

	cfg := s.cfg.Get()
	opts := match.Options{
		Accept: cfg.MatchAccept,
		Maybe:  cfg.MatchMaybe,
		Weights: match.Weights{
			Title:      cfg.MatchWeight.Title,
			Artist:     cfg.MatchWeight.Artist,
			Duration:   cfg.MatchWeight.Duration,
			Popularity: cfg.MatchWeight.Popularity,
			Version:    cfg.MatchWeight.Version,
		},
	}

	res, err := match.Find(ctx, s.spotify, req, opts)
	if err != nil {
		code, text := errs.Describe(err)
		s.log.Error("поиск трека не удался", "заказ", r.UserInput, "код", code, "ошибка", err)
		s.state.SetOrderMatch(r.ID, app.OrderMatch{State: app.MatchFailed, Note: text})
		return
	}

	if !res.Found {
		// Честный отказ лучше случайного трека: дальше такой заказ уйдёт на
		// YouTube, а пока просто говорим, что не нашли.
		s.log.Info("трек не найден",
			"заказ", r.UserInput, "артист", req.Artist, "название", req.Title,
			"запросы", res.Attempts, "кандидатов", res.Considered)
		s.state.SetOrderMatch(r.ID, app.OrderMatch{
			State: app.MatchMissing,
			Note:  "В Spotify не нашлось",
		})
		return
	}

	s.log.Info("трек найден",
		"заказ", r.UserInput,
		"трек", res.Track.Artists[0]+" — "+res.Track.Title,
		"оценка", res.Score.Total, "почему", res.Score.Why,
		"неточно", res.Uncertain, "кандидатов", res.Considered)

	// В памяти держим только уверенные ответы: сомнительный подбор
	// закрепится и будет повторяться до конца стрима.
	if !res.Uncertain {
		if err := s.matchCache.Put(ctx, key, match.Hit{
			TrackID: res.Track.ID,
			Title:   res.Track.Title,
			Artist:  res.Track.Artists[0],
			Score:   res.Score.Total,
		}); err != nil {
			s.log.Warn("не запомнил подбор", "ошибка", err)
		}
	}

	state := app.MatchFound
	note := ""
	if res.Uncertain {
		state = app.MatchUncertain
		note = "Совпадение неточное — проверь, тот ли трек"
	}

	s.state.SetOrderMatch(r.ID, app.OrderMatch{
		State:    state,
		TrackID:  res.Track.ID,
		URI:      res.Track.URI,
		Title:    res.Track.Title,
		Artist:   res.Track.Artists[0],
		CoverURL: res.Track.CoverURL,
		Duration: res.Track.DurationMs,
		Note:     note,
		Why:      res.Score.Why,
	})
}

func memoryNote(manual bool) string {
	if manual {
		return "Из твоего исправления"
	}
	return ""
}

// matchOptionsFromConfig нужен тестам и панели, чтобы не собирать структуру
// вручную в двух местах.
func matchOptionsFromConfig(cfg config.Config) match.Options {
	return match.Options{
		Accept: cfg.MatchAccept,
		Maybe:  cfg.MatchMaybe,
		Weights: match.Weights{
			Title:      cfg.MatchWeight.Title,
			Artist:     cfg.MatchWeight.Artist,
			Duration:   cfg.MatchWeight.Duration,
			Popularity: cfg.MatchWeight.Popularity,
			Version:    cfg.MatchWeight.Version,
		},
	}
}
