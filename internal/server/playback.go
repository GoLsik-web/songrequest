package server

import (
	"context"
	"strconv"
	"time"

	"songrequest/internal/app"
	"songrequest/internal/config"
	"songrequest/internal/errs"
	"songrequest/internal/player"
	"songrequest/internal/queue"
	"songrequest/internal/spotify"
)

// setupPlayer собирает плеер и подписывает панель на его события.
func (s *Server) setupPlayer(cfg *config.File) {
	s.player = player.New(s.queue, s.spotify, s.log)
	s.player.ResumeDelay = time.Duration(cfg.Get().ResumeDelaySeconds) * time.Second
	if s.player.ResumeDelay <= 0 {
		s.player.ResumeDelay = 3 * time.Second
	}

	s.player.OnChange = s.syncPlayback
	s.player.OnError = func(err error) { s.state.NotifyError(err) }

	// Трек отыграл — помечаем заказ выполненным на Twitch, чтобы он ушёл из
	// очереди наград, и складываем его в историю.
	s.player.OnFinished = func(item queue.Item) {
		s.history(item, "played", "")
		if item.RedemptionID != "" {
			ctx, cancel := context.WithTimeout(s.baseContext(), 20*time.Second)
			defer cancel()
			if err := s.twitch.FulfillRedemption(ctx, item.RewardID, item.RedemptionID); err != nil {
				s.log.Warn("не отметил заказ выполненным", "ошибка", err)
			}
		}
	}

	// Очередь опустела и музыка вернулась на место — говорим об этом в панели
	// и, если источник вернуть не вышло, дозаполняем по выбранному режиму.
	s.player.OnRestored = func(outcome spotify.RestoreOutcome, snap *spotify.Snapshot) {
		if outcome.Restored {
			s.state.Notify("info", outcome.Message)
		} else if outcome.Code != "" {
			s.state.NotifyCode("warn", string(outcome.Code), outcome.Message)
		}
		ctx, cancel := context.WithTimeout(s.baseContext(), 30*time.Second)
		defer cancel()
		s.afterRestore(ctx, snap, outcome)
	}
}

// StartPlayer запускает проигрывание очереди.
func (s *Server) StartPlayer(ctx context.Context) {
	if s.player == nil {
		return
	}
	go s.player.Run(ctx)
	s.syncPlayback()
}

// syncPlayback переносит состояние очереди и плеера в панель.
func (s *Server) syncPlayback() {
	if s.player == nil || s.queue == nil {
		return
	}

	items, err := s.queue.List()
	if err != nil {
		s.log.Warn("не прочитал очередь", "ошибка", err)
		return
	}

	view := make([]app.QueueItem, 0, len(items))
	for _, it := range items {
		view = append(view, app.QueueItem{
			ID:         it.ID,
			Source:     it.Source,
			Requester:  it.Requester,
			Title:      it.Title,
			Artist:     it.Artist,
			Provider:   it.Provider,
			DurationMs: it.DurationMs,
			Uncertain:  it.Uncertain,
			RawRequest: it.RawRequest,
			CoverURL:   it.CoverURL,
		})
	}
	s.state.SetQueue(view)
	s.state.SetPaused(s.player.Paused())

	now := s.player.Now()
	if now == nil {
		s.state.SetNow(nil)
	} else {
		s.state.SetNow(&app.NowPlaying{
			Provider:   now.Item.Provider,
			Title:      now.Item.Title,
			Artist:     now.Item.Artist,
			CoverURL:   now.Item.CoverURL,
			Requester:  now.Item.Requester,
			PositionMs: int(now.Elapsed().Milliseconds()),
			DurationMs: now.Item.DurationMs,
			Uncertain:  now.Item.Uncertain,
		})
	}

	// Снимок живёт в плеере: панель показывает, куда вернётся музыка.
	snap := s.player.Snapshot()
	s.state.UpdateSpotify(func(info *app.SpotifyInfo) {
		if snap == nil {
			info.SnapshotText = ""
			info.SnapshotAt = nil
			return
		}
		at := snap.CapturedAt
		info.SnapshotText = snap.Describe()
		info.SnapshotAt = &at
	})
}

// enqueue ставит подобранный трек в очередь, проверив его фильтрами.
//
// Отказ — не молчаливый: зритель получает объяснение в чате и баллы обратно,
// стример видит причину в панели. Заказ, за который списали баллы и ничего не
// сказали, — это жалоба в чат через минуту.
func (s *Server) enqueue(ctx context.Context, item queue.Item) {
	cfg := s.cfg.Get()

	if reject := s.check(item.Requester, item, cfg); reject != nil {
		s.log.Info("заказ отклонён",
			"зритель", item.Requester, "заказ", item.RawRequest, "причина", reject.Reason)
		s.history(item, "rejected", reject.Reason)
		s.refund(ctx, item, reject.Reason)
		s.state.NotifyWarn(errs.Code(""), "Отказ · "+item.Requester+": "+reject.Reason)
		s.say(ctx, "@"+item.Requester+", заказ не принят: "+reject.Reason)
		return
	}

	added, err := s.queue.Add(item, cfg.DonationPriority)
	if err != nil {
		s.log.Error("не смог поставить заказ в очередь", "ошибка", err)
		s.refund(ctx, item, "у приложения не получилось поставить трек в очередь")
		return
	}

	s.log.Info("заказ в очереди",
		"позиция", added.Position, "трек", added.Artist+" — "+added.Title,
		"заказал", added.Requester)

	s.syncPlayback()
	s.player.Nudge()

	// Зритель должен понимать, что заказ принят и когда его ждать: иначе
	// через минуту он спросит об этом в чате.
	wait, _ := s.queue.TotalDuration()
	if now := s.player.Now(); now != nil {
		wait += time.Duration(now.Item.DurationMs)*time.Millisecond - now.Elapsed()
	}
	s.announceQueued(ctx, added, added.Position, wait)
}

// refund возвращает баллы за заказ и говорит зрителю, почему.
func (s *Server) refund(ctx context.Context, item queue.Item, reason string) {
	if item.RedemptionID == "" {
		return // заказ пришёл не за баллы — возвращать нечего
	}
	if err := s.twitch.RefundRedemption(ctx, item.RewardID, item.RedemptionID); err != nil {
		s.log.Error("не смог вернуть баллы",
			"зритель", item.Requester, "причина_отказа", reason, "ошибка", err)
		s.state.NotifyError(err)
		return
	}
	s.log.Info("баллы возвращены", "зритель", item.Requester, "причина", reason)
}

// skipCurrent обрывает текущий трек.
func (s *Server) skipCurrent(actor string) {
	now := s.player.Now()
	if now == nil {
		return
	}
	s.history(now.Item, "skipped", "скипнул "+actor)
	s.modLog(actor, "скипнул", now.Item.Artist+" — "+now.Item.Title)
	s.state.Notify("info", actor+" скипнул: "+now.Item.Artist+" — "+now.Item.Title)
	s.player.Skip()
}

// removeFromQueue убирает заказ, при желании вернув за него баллы.
func (s *Server) removeFromQueue(ctx context.Context, id int64, refund bool, actor string) error {
	item, err := s.queue.Get(id)
	if err != nil {
		return err
	}
	if err := s.queue.Remove(id); err != nil {
		return err
	}

	what := "удалил"
	if refund {
		what = "удалил и вернул баллы"
		s.refund(ctx, item, "заказ удалён из очереди")
	}
	s.history(item, "removed", what)
	s.modLog(actor, what, item.Artist+" — "+item.Title)
	s.state.Notify("info", actor+" "+what+": "+item.Artist+" — "+item.Title)

	s.syncPlayback()
	return nil
}

// clearQueue очищает очередь целиком.
func (s *Server) clearQueue(ctx context.Context, refund bool, actor string) error {
	gone, err := s.queue.Clear()
	if err != nil {
		return err
	}
	for _, item := range gone {
		if refund {
			s.refund(ctx, item, "очередь очищена")
		}
		s.history(item, "removed", "очередь очищена")
	}

	s.modLog(actor, "очистил очередь", strconv.Itoa(len(gone))+" заказов")
	s.state.Notify("info", actor+" очистил очередь: "+strconv.Itoa(len(gone))+" "+
		plural(len(gone), "заказ", "заказа", "заказов"))

	s.syncPlayback()
	return nil
}
