package server

import (
	"context"
	"time"

)

// Подхват пропущенных заказов.
//
// EventSub присылает только то, что случилось, пока подписка жива. Всё, что
// зрители нажали за время перезапуска или обрыва связи, оставалось висеть на
// Twitch: баллы списаны, трека нет, и никто об этом не знает. 12.10 так
// пропали два заказа за минуту простоя.
//
// Поэтому при каждом подключении спрашиваем у Twitch невыполненные заказы и
// разбираем те, которых приложение ещё не видело.

// staleRedemption — заказ старше этого не играем, а возвращаем баллы:
// скорее всего, он с прошлого стрима или простой был долгим, и трек уже
// никому не нужен.
const staleRedemption = 30 * time.Minute

// markSeen отмечает заказ увиденным. false — его уже разбирали: EventSub
// умеет присылать событие повторно, а подхват может встретить заказ, который
// прямо сейчас ищется.
func (s *Server) markSeen(id string) bool {
	if id == "" {
		return true
	}
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	if s.seen[id] {
		return false
	}
	s.seen[id] = true
	return true
}

// catchUpRedemptions разбирает заказы, пропущенные, пока приложение не слушало.
func (s *Server) catchUpRedemptions(ctx context.Context, rewardIDs []string) {
	// Дать подписке встать: что придёт по ней, не надо брать вторым путём.
	select {
	case <-ctx.Done():
		return
	case <-time.After(3 * time.Second):
	}

	inQueue := map[string]bool{}
	if s.queue != nil {
		if items, err := s.queue.List(); err == nil {
			for _, it := range items {
				inQueue[it.RedemptionID] = true
			}
		}
	}

	for _, rewardID := range rewardIDs {
		qctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		list, err := s.twitch.UnfulfilledRedemptions(qctx, rewardID)
		cancel()
		if err != nil {
			s.log.Warn("не спросил у Twitch пропущенные заказы", "ошибка", err)
			continue
		}
		for _, r := range list {
			// В очереди он ждёт своей очереди — это не пропущенный.
			if inQueue[r.ID] {
				s.markSeen(r.ID)
				continue
			}
			if !s.markSeen(r.ID) {
				continue
			}
			if !r.RedeemedAt.IsZero() && time.Since(r.RedeemedAt) > staleRedemption {
				s.log.Info("старый невыполненный заказ — возвращаю баллы",
					"зритель", r.UserLogin, "текст", r.UserInput, "когда", r.RedeemedAt)
				s.rejectRedemption(ctx, r, "заказ пролежал, пока приложение было выключено")
				continue
			}
			s.log.Info("подхватил заказ, сделанный пока приложение не слушало",
				"зритель", r.UserLogin, "текст", r.UserInput)
			s.handleRedemption(r)
		}
	}
}

