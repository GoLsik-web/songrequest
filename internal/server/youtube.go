package server

import (
	"context"
	"strings"

	"songrequest/internal/app"
	"songrequest/internal/errs"
	"songrequest/internal/links"
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

		// Первый запуск качает yt-dlp и mpv — вместе это тридцать с лишним
		// мегабайт и до минуты времени. Пока идёт загрузка, в панели должно
		// быть написано, что происходит: иначе там просто «не готов», и
		// человек решает, что сломалось.
		s.state.UpdateYouTube(func(y *app.YouTubeInfo) {
			y.Ready = false
			y.Note = "Готовлю запасной проигрыватель: качаю yt-dlp и mpv (около 35 МБ). Это разовая загрузка."
			y.NoteCode = ""
		})

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

// Подписи под заказом, который играется мимо Spotify. Их две, потому что
// причины разные, и стример по подписи должен понять, что происходит:
// «в Spotify такого нет» — обычное дело, «Spotify не ответил» — поломка,
// про которую он захочет узнать.
const (
	noteYouTubeMissing  = "Играем с YouTube — в Spotify такого нет"
	noteYouTubeNoAnswer = "Играем с YouTube: Spotify не ответил на поиск"
)

// tryYouTube ищет заказ на YouTube и ставит его в очередь.
// false означает «не вышло, возвращай баллы».
func (s *Server) tryYouTube(ctx context.Context, r twitch.Redemption, query, note string) bool {
	if s.youtube == nil || !s.ytTools.Ready() {
		return false
	}

	// По ссылке не ищем: зритель уже сказал, что именно хочет. Но отдавать
	// yt-dlp то, что зритель написал, нельзя — только собранный нами заново
	// адрес.
	//
	// Раньше здесь стояла проверка «в тексте есть youtube.com/» и текст
	// уезжал в yt-dlp как есть. Зритель мог написать заказ вида
	// «--config-location=\чужой-сервер\yt.conf youtube.com/»: это один
	// элемент командной строки, начинающийся с двух минусов, и yt-dlp
	// разбирает его как свой ключ, а не как адрес ролика. То есть чужой
	// человек через заказ за баллы подсовывал стримеру настройки yt-dlp на
	// его же компьютере — а yt-dlp у нас ещё и ходит в куки браузера.
	// links.Find достаёт из текста только одиннадцать знаков
	// идентификатора и собирает адрес сам, так что подставить туда нечего.
	var (
		track *youtube.Track
		err   error
	)
	if link, ok := links.Find(r.UserInput); ok && link.Kind == links.YouTube {
		track, err = s.youtube.Lookup(ctx, link.URL)
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
		Note:     note,
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
	s.youtube.SetOptions(cfg.AudioDevice, cfg.YouTubeBrowser, cfg.YouTubeVolume)
}
