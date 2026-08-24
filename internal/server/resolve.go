package server

import (
	"context"
	"time"

	"songrequest/internal/app"
	"songrequest/internal/config"
	"songrequest/internal/errs"
	"songrequest/internal/match"
	"songrequest/internal/queue"
	"songrequest/internal/twitch"
)

// resolveTimeout — сколько ждём подбор трека. Пять запросов к Spotify с
// повторами укладываются с запасом, а зритель не должен ждать минуту.
const resolveTimeout = 25 * time.Second

// resolveOrder ищет заказанный трек в Spotify и ставит его в очередь.
//
// Не нашли — баллы возвращаются, и зритель получает объяснение в чат. Заказ,
// за который списали баллы и промолчали, — это жалоба в чат через минуту.
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
		s.rejectRedemption(ctx, r, "ты не написал, что заказываешь")
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
		s.enqueue(ctx, queue.Item{
			Source: queue.SourcePoints, Requester: r.UserName, RawRequest: r.UserInput,
			Provider: "spotify", TrackID: hit.TrackID, URI: "spotify:track:" + hit.TrackID,
			Title: hit.Title, Artist: hit.Artist, DurationMs: hit.DurationMs,
			CoverURL: hit.CoverURL, RedemptionID: r.ID, RewardID: r.RewardID,
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
		s.rejectRedemption(ctx, r, "не получилось поискать трек, попробуй ещё раз")
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
		s.rejectRedemption(ctx, r, "не нашёл такого трека в Spotify")
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

	s.enqueue(ctx, queue.Item{
		Source:       queue.SourcePoints,
		Requester:    r.UserName,
		RawRequest:   r.UserInput,
		Provider:     "spotify",
		TrackID:      res.Track.ID,
		URI:          res.Track.URI,
		Title:        res.Track.Title,
		Artist:       res.Track.Artists[0],
		DurationMs:   res.Track.DurationMs,
		CoverURL:     res.Track.CoverURL,
		Uncertain:    res.Uncertain,
		RedemptionID: r.ID,
		RewardID:     r.RewardID,
	})
}

// rejectRedemption возвращает баллы и объясняет зрителю, почему.
func (s *Server) rejectRedemption(ctx context.Context, r twitch.Redemption, reason string) {
	if r.ID != "" && r.RewardID != "" {
		if err := s.twitch.RefundRedemption(ctx, r.RewardID, r.ID); err != nil {
			s.log.Error("не смог вернуть баллы за неудачный заказ", "ошибка", err)
			s.state.NotifyError(err)
		} else {
			s.state.SetRedemptionStatus(r.ID, app.OrderRefunded)
		}
	}
	s.say(ctx, "@"+r.UserName+", "+reason+". Баллы вернул.")
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
