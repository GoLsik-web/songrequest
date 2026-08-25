package server

import (
	"context"
	"time"

	"songrequest/internal/app"
)

// Своя музыка стримера в виджете.
//
// Приложение живёт заказами и про фоновый плейлист ничего не знает: очередь
// пуста — значит показывать нечего. Но в кадре зритель хочет видеть «что
// играет», а не «что заказали», поэтому в паузах между заказами мы
// спрашиваем у Spotify, что там сейчас.
//
// Спрашиваем, а не подписываемся, потому что у Spotify нет уведомлений о
// смене трека — только опрос. Поэтому опрос устроен так, чтобы стоить как
// можно меньше:
//
//   - молчим, пока играет заказ: там трек известен точно и без запросов;
//   - молчим, если стример выключил показ своей музыки;
//   - молчим, если Spotify не подключён.
//
// Шесть секунд — компромисс: смена трека в кадре отстаёт максимум на этот
// срок, а за час стрима набегает шестьсот запросов при лимите, который
// Spotify считает тысячами.

const ownPollEvery = 6 * time.Second

// watchOwnPlayback держит в состоянии то, что стример слушает сам.
func (s *Server) watchOwnPlayback(ctx context.Context) {
	tick := time.NewTicker(ownPollEvery)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.pollOwn(ctx)
		}
	}
}

func (s *Server) pollOwn(ctx context.Context) {
	// Играет заказ — своя музыка сейчас не звучит, и спрашивать не о чем.
	if s.player.Now() != nil {
		s.setOwn(nil)
		return
	}
	if !s.cfg.Get().Widget.ShowOwn || !s.spotify.Connected() {
		s.setOwn(nil)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	st, ok, err := s.spotify.State(ctx)
	if err != nil {
		// Обычное дело: сеть моргнула, токен обновляется, Spotify молчит.
		// Шуметь об этом в лог каждые шесть секунд незачем — уровень отладки.
		s.log.Debug("не прочитал, что играет у стримера", "ошибка", err)
		return
	}
	if !ok || st == nil || st.Item == nil || !st.IsPlaying {
		s.setOwn(nil)
		return
	}

	artist := ""
	if len(st.Item.Artists) > 0 {
		artist = st.Item.Artists[0].Name
	}
	cover := ""
	if len(st.Item.Album.Images) > 0 {
		cover = st.Item.Album.Images[0].URL
	}

	s.setOwn(&app.NowPlaying{
		Provider:   "spotify",
		Source:     app.SourceOwn,
		Title:      st.Item.Name,
		Artist:     artist,
		CoverURL:   cover,
		PositionMs: st.ProgressMs,
		DurationMs: st.Item.DurationMs,
	})
}

// setOwn запоминает свою музыку и рассылает состояние.
//
// Рассылаем на каждый опрос, а не только при смене трека, и вот почему:
// вместе с треком едет его позиция, а по ней страницы заводят свой отсчёт
// времени. Без регулярной рассылки только что открытая панель получала
// позицию той давности, когда трек начался, и полоса вставала не на своё
// место. Дёргаться от этого нечему: и панель, и виджет сверяют, что именно
// изменилось, и лишнего не перерисовывают.
func (s *Server) setOwn(now *app.NowPlaying) {
	s.mu.Lock()
	had := s.ownNow != nil
	s.ownNow = now
	s.mu.Unlock()

	// Тишина, которая была тишиной и осталась, — единственный случай, когда
	// рассылать нечего.
	if now == nil && !had {
		return
	}
	s.syncPlayback()
}

// own отдаёт то, что стример слушает сам, если это сейчас уместно.
func (s *Server) own() *app.NowPlaying {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ownNow
}

// ownTrackLeft — сколько осталось до конца трека, который стример слушает сам.
//
// Берём из того же опроса, что кормит виджет: лишний запрос к Spotify ради
// оценки в чате не нужен, а точность до шести секунд здесь никому не важна.
func (s *Server) ownTrackLeft() time.Duration {
	own := s.own()
	if own == nil || own.DurationMs <= own.PositionMs {
		return 0
	}
	return time.Duration(own.DurationMs-own.PositionMs) * time.Millisecond
}
