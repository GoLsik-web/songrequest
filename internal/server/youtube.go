package server

import (
	"context"
	"strings"

	"songrequest/internal/app"
	"songrequest/internal/errs"
	"songrequest/internal/queue"
	"songrequest/internal/twitch"
	"songrequest/internal/youtube"
)

// setupYouTube готовит запасной проигрыватель.
//
// Инструменты ищутся и качаются в фоне: первый запуск не должен ждать
// загрузки, а без YouTube приложение прекрасно работает — просто часть
// заказов не сыграет.
func (s *Server) setupYouTube(ctx context.Context) {
	s.ytTools = youtube.NewTools(s.log, s.dataDir)
	s.youtube = youtube.NewPlayer(s.log, s.ytTools)
	s.applyYouTubeSettings()

	if s.player != nil {
		s.player.SetYouTube(s.youtube)
	}

	go func() {
		cfg := s.cfg.Get()
		if err := s.ytTools.Ensure(ctx, cfg.YtDlpPath, cfg.MpvPath); err != nil {
			code, text := errs.Describe(err)
			s.log.Warn("запасной проигрыватель не готов", "код", code, "ошибка", err)
			s.state.UpdateYouTube(func(y *app.YouTubeInfo) {
				y.Ready = false
				y.Note = text
				y.NoteCode = string(code)
			})
			return
		}

		ytdlp, mpv := s.ytTools.Paths()
		s.log.Info("запасной проигрыватель готов", "yt-dlp", ytdlp, "mpv", mpv)
		s.state.UpdateYouTube(func(y *app.YouTubeInfo) {
			y.Ready = true
			y.YtDlp = ytdlp
			y.Mpv = mpv
			y.Device = s.cfg.Get().AudioDevice
			y.Note = ""
			y.NoteCode = ""
		})

		// Список устройств нужен, чтобы стример выбрал виртуальный кабель
		// из списка, а не вписывал его имя руками с ошибкой в регистре.
		if devices, err := s.youtube.Devices(ctx); err == nil {
			s.state.UpdateYouTube(func(y *app.YouTubeInfo) { y.Devices = devices })
		}
	}()
}

// tryYouTube ищет заказ на YouTube и ставит его в очередь.
// false означает «не вышло, возвращай баллы».
func (s *Server) tryYouTube(ctx context.Context, r twitch.Redemption, query string) bool {
	if s.youtube == nil || !s.ytTools.Ready() {
		return false
	}

	// Ссылку отдаём как есть: искать по ней бессмысленно, зритель уже
	// сказал, что именно хочет.
	var (
		track *youtube.Track
		err   error
	)
	if youtube.IsLink(r.UserInput) {
		track, err = s.youtube.Lookup(ctx, r.UserInput)
	} else {
		track, err = s.youtube.Search(ctx, query)
	}
	if err != nil {
		s.log.Info("на YouTube тоже не нашлось", "заказ", r.UserInput, "ошибка", err)
		return false
	}

	// Стрим — не музыка, и играть его нельзя: он не кончится.
	if track.IsLive {
		s.log.Info("заказ оказался стримом", "заказ", r.UserInput)
		return false
	}

	s.state.SetOrderMatch(r.ID, app.OrderMatch{
		State:    app.MatchFound,
		TrackID:  track.ID,
		URI:      track.URL,
		Title:    track.Title,
		Artist:   track.Artist,
		CoverURL: track.CoverURL,
		Duration: track.DurationMs,
		Note:     "Играем с YouTube — в Spotify такого нет",
	})

	s.enqueue(ctx, queue.Item{
		Source:    queue.SourcePoints,
		Requester: r.UserName,
		// Бан-лист работает по логину. Без него забаненный зритель спокойно
		// заказывал всё, чего нет в Spotify: заказ уходил на YouTube, а
		// проверка бана откатывалась на отображаемое имя и не срабатывала.
		RequesterLogin: strings.ToLower(r.UserLogin),
		RawRequest:     r.UserInput,
		Provider:       "youtube",
		TrackID:        track.ID,
		URI:            track.URL,
		Title:          track.Title,
		Artist:         track.Artist,
		DurationMs:     track.DurationMs,
		CoverURL:       track.CoverURL,
		RedemptionID:   r.ID,
		RewardID:       r.RewardID,
	})
	return true
}

// StartYouTube поднимает запасной проигрыватель.
func (s *Server) StartYouTube(ctx context.Context) { s.setupYouTube(ctx) }

// StopYouTube убивает mpv при закрытии приложения: иначе он останется
// занимать звуковое устройство после того, как приложение закрыли.
func (s *Server) StopYouTube() {
	if s.youtube != nil {
		s.youtube.Stop()
	}
}

// applyYouTubeSettings переносит настройки в проигрыватель YouTube.
//
// Раньше они читались один раз при запуске, а панель обещала «применится к
// следующему заказу». Обещание было ложным: до перезапуска ничего не менялось.
func (s *Server) applyYouTubeSettings() {
	if s.youtube == nil {
		return
	}
	cfg := s.cfg.Get()
	s.youtube.SetOptions(cfg.AudioDevice, cfg.YouTubeBrowser)
}
