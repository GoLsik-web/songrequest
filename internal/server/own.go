package server

import (
	"context"
	"strings"
	"time"

	"songrequest/internal/app"
	"songrequest/internal/spotify"
	"songrequest/internal/spotifyapp"
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
// Шаг опроса зависит от того, смотрит ли кто-нибудь в панель.
//
// Шесть секунд — экономный шаг для виджета: смена трека в кадре отстаёт
// максимум на этот срок.
//
// Две секунды — пока панель открыта. Там стример видит полосу времени и
// перематывает трек прямо в Spotify; с шестисекундным шагом полоса ехала
// следом с заметным опозданием, и выглядело это как «панель врёт».
//
// Была одна секунда, и это оказалось слишком жадно. Живьём: 29.08 Spotify
// ответил приложению «подождите четыре с половиной часа» — заказы встали на
// весь вечер. У приложения в режиме разработки (а другого у нас не будет:
// Client ID стример заводит на своём аккаунте) норма запросов тесная, и
// шестьдесят в минуту часами она не выдерживает. Полосу времени панель и так
// отсчитывает у себя, между ответами, — от двух секунд вместо одной человек
// не заметит ничего, кроме того, что Spotify перестал ругаться.
const (
	ownPollFast = 2 * time.Second
	ownPollSlow = 6 * time.Second

	// ownPollCalm — шаг после того, как Spotify пожаловался на частоту.
	// Держится десять минут (calmFor в internal/spotify), потом сам
	// возвращается к обычному.
	ownPollCalm = 15 * time.Second
)

// watchOwnPlayback держит в состоянии то, что стример слушает сам.
func (s *Server) watchOwnPlayback(ctx context.Context) {
	// Состояние опроса живёт здесь, в единственной горутине, которая его
	// читает и меняет. Полями сервера оно быть не должно: к ним лезут
	// обработчики панели из десятка других горутин.
	var w ownWatch

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.pollStep()):
			// Заодно переставляем шаг проверки играющего заказа: он живёт в
			// плеере и сам про жалобы Spotify не знает.
			s.applyPollStep()
			s.notePause()
			s.pollOwn(ctx, &w)
		}
	}
}

// readLocal читает играющий трек у программы Spotify на этом компьютере.
//
// Через поле, а не напрямую, ради проверок: на машине разработчика Spotify
// тоже запущен, и тесты ловили бы его настоящий трек вместо выдуманного.
func (s *Server) readLocal() (spotifyapp.Track, spotifyapp.Status) {
	if s.localTrack != nil {
		return s.localTrack()
	}
	return spotifyapp.Read()
}

// pollStep — с каким шагом сейчас заглядывать, что играет.
//
// Шаг дешёвый: почти всегда это чтение заголовка окна программы Spotify, без
// всякой сети. По сети приложение ходит несравнимо реже — см. ownRefreshPanel.
func (s *Server) pollStep() time.Duration {
	// Spotify недавно просил сбавить темп — сбавляем. Продолжать в прежнем
	// темпе значит выпросить паузу на часы, а с ней встанут и заказы.
	if s.spotify != nil && s.spotify.RecentlyLimited() {
		return ownPollCalm
	}
	if s.panels.Load() > 0 {
		return ownPollFast
	}
	return ownPollSlow
}

// panelOpened и panelClosed считают открытые панели и заодно переводят на тот
// же шаг проверку играющего заказа: перемотка внутри заказа обязана доезжать
// до полосы так же быстро, как перемотка своей музыки.
func (s *Server) panelOpened() {
	s.panels.Add(1)
	s.applyPollStep()
}

func (s *Server) panelClosed() {
	s.panels.Add(-1)
	s.applyPollStep()
}

func (s *Server) applyPollStep() {
	if s.player != nil {
		s.player.SetPollEvery(s.pollStep())
	}
}

// ownWatch — что мы уже знаем про свою музыку стримера.
//
// Смысл всей этой памяти в том, чтобы не спрашивать Spotify по сети чаще, чем
// нужно. Имя трека приходит бесплатно из заголовка окна программы Spotify, а
// по сети мы ходим только за тем, чего в заголовке нет: обложкой,
// длительностью и точным положением.
type ownWatch struct {
	// track — что показывает программа Spotify прямо сейчас.
	track spotifyapp.Track
	// remote — когда последний раз спрашивали Spotify по сети без подсказки
	// от программы: когда её нет или музыка в ней стоит.
	remote time.Time
	// now — подробности, добытые по сети для этого трека. Пусто, если Spotify
	// недоступен: тогда в панели будут только артист и название.
	now *app.NowPlaying
	// at и pos — когда и на каком месте трека мы последний раз спрашивали
	// Spotify. Между запросами положение считаем сами.
	at  time.Time
	pos int
	// asked — когда последний раз ходили за подробностями. Нужно, чтобы
	// подхватывать перемотку внутри трека, но не каждую секунду.
	asked time.Time
}

// forget сбрасывает всё, что знали про трек. Время последнего запроса по сети
// остаётся: оно про частоту запросов, а не про трек.
func (w *ownWatch) forget() {
	remote := w.remote
	*w = ownWatch{remote: remote}
}

// ownRefreshPanel и ownRefreshWidget — как часто уточнять положение по сети,
// пока играет один и тот же трек.
//
// Само имя трека уточнять не нужно: оно приходит из заголовка окна мгновенно
// и бесплатно. По сети мы ходим только за перемоткой — стример может двинуть
// ползунок в самом Spotify, и полоса в панели обязана поехать следом.
//
// Раньше здесь была секунда, и на ней всё и погорело: 29.08 Spotify закрыл
// приложению доступ к плееру на четыре с половиной часа, потому что за вечер
// стрима таких запросов набегают тысячи.
const (
	ownRefreshPanel  = 15 * time.Second
	ownRefreshWidget = 60 * time.Second
)

// pollOwn обновляет то, что стример слушает сам.
func (s *Server) pollOwn(ctx context.Context, w *ownWatch) {
	// Играет заказ — своя музыка сейчас не звучит, и спрашивать не о чем.
	if s.player.Now() != nil {
		w.forget()
		s.setOwn(nil)
		return
	}
	if !s.cfg.Get().Widget.ShowOwn || !s.spotify.Connected() {
		w.forget()
		s.setOwn(nil)
		return
	}

	// Сначала спрашиваем программу Spotify на этом же компьютере: это не
	// стоит ничего и работает даже тогда, когда Spotify закрыл приложению
	// доступ по сети.
	track, status := s.readLocal()
	if status == spotifyapp.Playing {
		s.pollOwnLocal(ctx, w, track)
		return
	}

	// Программа молчит или её нет вовсе. Спрашивать по сети всё-таки надо —
	// музыка могла играть с телефона или из браузера, — но редко: именно
	// частые запросы и довели 29.08 до четырёхчасовой паузы.
	remembered := w.track
	w.forget()
	w.track = spotifyapp.Track{}

	if time.Since(w.remote) < ownRefreshWidget {
		// Между редкими запросами ничего не трогаем: мигать в кадре нечем.
		// Но если только что играл трек из программы Spotify, а теперь она
		// молчит, — тишину показать надо сразу.
		if status == spotifyapp.Idle && !remembered.Empty() {
			s.setOwn(nil)
		}
		return
	}
	w.remote = time.Now()
	s.pollOwnRemote(ctx)
}

// pollOwnLocal ведёт трек, имя которого мы прочитали у программы Spotify.
func (s *Server) pollOwnLocal(ctx context.Context, w *ownWatch, track spotifyapp.Track) {
	fresh := !track.Same(w.track)
	if fresh {
		// Трек сменился — всё, что мы знали про прошлый, больше не годится.
		w.forget()
		w.track = track
		// Заодно снимаем свою же отметку о паузе: музыка едет дальше, значит
		// на паузе она не стоит. Без этого кнопка в панели навсегда осталась
		// бы «продолжить» — стример снял бы паузу в самом Spotify, а панель
		// продолжала обещать обратное.
		s.ownPaused.Store(false)
	}

	if fresh || time.Since(w.asked) > s.ownRefresh() {
		w.asked = time.Now()
		s.fetchOwnDetails(ctx, w, track)
	}

	// Пока музыка стоит на паузе, точку отсчёта двигаем вместе с часами —
	// тогда положение остаётся там, где остановились. Иначе полоса в панели
	// продолжала бы ехать по стоящему треку и добралась бы до конца.
	if s.ownPaused.Load() && !w.at.IsZero() {
		w.at = time.Now()
	}

	s.setOwn(w.playing(track))
}

// ownRefresh — как часто уточнять положение по сети.
func (s *Server) ownRefresh() time.Duration {
	if s.panels.Load() > 0 {
		return ownRefreshPanel
	}
	return ownRefreshWidget
}

// fetchOwnDetails спрашивает у Spotify то, чего нет в заголовке окна:
// обложку, длительность и точное положение.
//
// Не получилось — не беда: в панели останутся артист и название, а полосы и
// обложки не будет. Это несравнимо лучше пустого места, которое человек видел
// раньше при любой заминке со Spotify.
func (s *Server) fetchOwnDetails(ctx context.Context, w *ownWatch, track spotifyapp.Track) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	st, ok, err := s.spotify.State(ctx)
	if err != nil {
		s.noteSpotifyError(err)
		s.log.Debug("не уточнил, что играет у стримера", "ошибка", err)
		return
	}
	if !ok || st == nil || st.Item == nil {
		return
	}

	// Spotify мог ответить про другой трек: заголовок окна меняется раньше,
	// чем это доезжает до его сервера. Тогда подробности не наши.
	if !sameTrack(track, st) {
		s.log.Debug("подробности пришли про другой трек",
			"в_окне", track.Artist+" — "+track.Title, "в_ответе", st.Item.Name)
		return
	}

	cover := ""
	if len(st.Item.Album.Images) > 0 {
		cover = st.Item.Album.Images[0].URL
	}
	w.now = &app.NowPlaying{
		Provider:   "spotify",
		Source:     app.SourceOwn,
		Title:      st.Item.Name,
		Artist:     track.Artist,
		CoverURL:   cover,
		DurationMs: st.Item.DurationMs,
	}
	w.pos, w.at = st.ProgressMs, time.Now()
}

// playing собирает то, что показать в панели и в кадре.
//
// Положение внутри трека считаем сами от последнего ответа Spotify: между
// запросами оно едет ровно так же, как едет музыка, и лишний запрос ради
// этого не нужен.
func (w *ownWatch) playing(track spotifyapp.Track) *app.NowPlaying {
	if w.now == nil {
		// Подробностей нет — показываем то, что знаем наверняка.
		return &app.NowPlaying{
			Provider: "spotify",
			Source:   app.SourceOwn,
			Title:    track.Title,
			Artist:   track.Artist,
		}
	}

	now := *w.now
	now.PositionMs = w.pos + int(time.Since(w.at)/time.Millisecond)
	if now.DurationMs > 0 && now.PositionMs > now.DurationMs {
		now.PositionMs = now.DurationMs
	}
	return &now
}

// sameTrack сверяет трек из заголовка окна с ответом Spotify.
//
// Сверяем по названию, а не по артисту: в заголовке Spotify пишет главного
// исполнителя, а в ответе их бывает несколько, и первым не всегда тот же.
func sameTrack(track spotifyapp.Track, st *spotify.PlayerState) bool {
	return strings.EqualFold(strings.TrimSpace(track.Title), strings.TrimSpace(st.Item.Name))
}

// pollOwnRemote — прежний путь: спросить Spotify по сети целиком.
//
// Нужен, когда программы Spotify на этом компьютере нет: стример слушает с
// телефона или из браузера. Тогда заголовок окна читать негде.
func (s *Server) pollOwnRemote(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	st, ok, err := s.spotify.State(ctx)
	if err != nil {
		s.noteSpotifyError(err)
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

// notePause показывает в карточке Spotify объявленную им паузу — и убирает,
// когда та кончилась.
//
// Карточка пересобирается по событиям (вход, проверка связи, смена настроек),
// а пауза приходит и уходит сама по себе. Без этой проверки человек видел
// живую карточку и не понимал, почему ничего не работает.
func (s *Server) notePause() {
	paused := s.spotify.PauseLeft() > 0
	if s.paused.Swap(paused) == paused {
		return
	}
	if paused {
		s.log.Warn("Spotify объявил паузу — опрос приостановлен")
	} else {
		s.log.Info("пауза Spotify кончилась")
	}
	s.syncSpotifyInfo()
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
