package server

import (
	"context"

	"songrequest/internal/config"
	"songrequest/internal/errs"
	"songrequest/internal/spotify"
)

// continuationLimit — сколько треков дозаполняем. Десяти хватает минут на
// сорок: за это время стример либо вернётся к музыке сам, либо придёт новый
// заказ. Больше — значит дольше держать чужой плеер занятым.
const continuationLimit = 10

// afterRestore доводит дело до конца, когда сам возврат отработал.
//
// Возврат бывает трёх видов, и каждый требует своего:
//   - вернулись полностью → ничего не делаем, музыка играет как играла;
//   - вернули трек, но не источник → трек доиграет и наступит тишина,
//     поэтому дозаполняем очередь Spotify по выбранному режиму;
//   - возвращать было нечего → включаем по выбранному режиму с нуля.
func (s *Server) afterRestore(ctx context.Context, snap *spotify.Snapshot, outcome spotify.RestoreOutcome) {
	cfg := s.cfg.Get()
	mode := cfg.EffectiveResumeMode()

	switch {
	case outcome.Restored && !outcome.ContextLost:
		return // всё на месте

	case outcome.Restored && outcome.ContextLost:
		s.fill(ctx, cfg, mode, snap, outcome.DeviceID)

	case outcome.Code == errs.SpotifyNothing:
		s.startFresh(ctx, cfg, mode, snap)
	}
}

// fill добавляет продолжение в очередь Spotify после возвращённого трека.
func (s *Server) fill(ctx context.Context, cfg config.Config, mode config.ResumeFailMode,
	snap *spotify.Snapshot, deviceID string) {

	if mode == config.ResumeNothing {
		s.state.NotifyWarn(errs.SpotifyRestore,
			"Вернул трек, но источник восстановить нельзя — после него музыка остановится.")
		return
	}

	uris, what, err := s.continuation(ctx, cfg, mode, snap)
	if err != nil {
		s.log.Warn("не собрал продолжение", "режим", mode, "ошибка", err)
		s.state.NotifyError(err)
		return
	}

	queued := 0
	for _, uri := range uris {
		if uri == snap.TrackURI {
			continue // он уже играет, второй раз не нужен
		}
		if err := s.spotify.QueueTrack(ctx, uri, deviceID); err != nil {
			s.log.Warn("не поставил трек в очередь Spotify", "ошибка", err)
			break
		}
		queued++
	}

	if queued > 0 {
		s.log.Info("дозаполнил воспроизведение", "режим", mode, "треков", queued)
		s.state.Notify("info", "Источник вернуть нельзя, поэтому дальше "+what+".")
	}
}

// startFresh включает музыку с нуля, когда возвращать было нечего.
func (s *Server) startFresh(ctx context.Context, cfg config.Config,
	mode config.ResumeFailMode, snap *spotify.Snapshot) {

	if mode == config.ResumeNothing {
		return // стример сам выбрал тишину
	}

	uris, what, err := s.continuation(ctx, cfg, mode, snap)
	if err != nil {
		s.log.Warn("не собрал, что включить", "режим", mode, "ошибка", err)
		s.state.NotifyError(err)
		return
	}

	if err := s.spotify.PlayTracks(ctx, uris, ""); err != nil {
		s.log.Warn("запасной вариант тоже не включился", "ошибка", err)
		s.state.NotifyError(err)
		return
	}
	s.log.Info("включил запасной вариант", "режим", mode, "треков", len(uris))
	s.state.Notify("info", "Возвращать было нечего, поэтому "+what+".")
}

// continuation собирает список треков по выбранному режиму и заодно фразу
// для панели — стример должен понимать, почему заиграло именно это.
func (s *Server) continuation(ctx context.Context, cfg config.Config,
	mode config.ResumeFailMode, snap *spotify.Snapshot) (uris []string, what string, err error) {

	switch mode {
	case config.ResumeFallbackPlaylist:
		uris, err = s.spotify.PlaylistTrackURIs(ctx, cfg.FallbackPlaylistID, continuationLimit)
		what = "играет запасной плейлист"
		if cfg.FallbackPlaylist != "" {
			what = "играет «" + cfg.FallbackPlaylist + "»"
		}

	case config.ResumeArtistRadio:
		if snap == nil || snap.ArtistID == "" {
			return nil, "", errs.New(errs.SpotifyNothing,
				"Не знаю, от какого артиста включать музыку — включать нечего.")
		}
		uris, err = s.spotify.ArtistTopTrackURIs(ctx, snap.ArtistID, continuationLimit)
		what = "играет музыка " + snap.ArtistName

	default:
		return nil, "", nil
	}

	if err != nil {
		return nil, "", err
	}
	if len(uris) == 0 {
		return nil, "", errs.New(errs.SpotifyNothing, "В выбранном источнике не нашлось треков.")
	}
	return uris, what, nil
}
