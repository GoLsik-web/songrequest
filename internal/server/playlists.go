package server

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"songrequest/internal/app"
	"songrequest/internal/config"
	"songrequest/internal/errs"
	"songrequest/internal/i18n"
	"songrequest/internal/links"
	"songrequest/internal/match"
	"songrequest/internal/queue"
	"songrequest/internal/spotify"
	"songrequest/internal/twitch"
)

// Заказ плейлиста — вторая награда на канале.
//
// ПОЧЕМУ ОТДЕЛЬНАЯ НАГРАДА, А НЕ «КИНЬ ПЛЕЙЛИСТ В ОБЫЧНУЮ». Две причины, обе
// назвал владелец. Первая — цена: плейлист должен быть выгоднее, чем те же
// треки поштучно, иначе его никто не закажет. Вторая — плейлист не идёт в
// эфир сам: сорок минут чужой музыки подряд решает хозяин эфира, а не зритель.
//
// ПОЭТОМУ ЗДЕСЬ ЕСТЬ ОЖИДАНИЕ. Заказанный плейлист не попадает в очередь. Он
// ложится сюда и ждёт, пока стример или модератор скажет «да» — кнопкой в
// панели или командой в чате. Одобрили — треки уходят в очередь подряд, в том
// порядке, в каком лежат в плейлисте. Отказали — баллы возвращаются.
//
// ЖДУЩИЕ ПЛЕЙЛИСТЫ ПЕРЕЖИВАЮТ ПЕРЕЗАПУСК. Строка JSON в таблице kv, как и
// «последний игравший трек». Иначе перезапуск приложения посреди стрима
// молча съедал бы чужие баллы: заказ оплачен, а решения по нему уже никто не
// примет.

// kvPlaylists — где лежат плейлисты, ждущие решения.
const kvPlaylists = "pending_playlists"

// playlistKeep — сколько ждущих плейлистов держим.
//
// Это не про память, а про человека: список, в котором больше десятка
// пунктов, посреди эфира никто разбирать не станет. Дальше отказываем сразу,
// с возвратом баллов, и зрителю честно сказано почему.
const playlistKeep = 10

// pendingTrack — один трек будущего заказа.
//
// У плейлиста Spotify тут сразу лежит готовый адрес: искать нечего, трек уже
// там, где играет. У Яндекса адреса нет — только имя артиста, название и
// длительность, — и трек придётся искать при одобрении. Разбирать это заранее
// нельзя: плейлист может провисеть в ожидании час, а поиск стоит запросов.
type pendingTrack struct {
	Artist     string `json:"artist"`
	Title      string `json:"title"`
	DurationMs int    `json:"duration_ms"`

	// Provider и URI заполнены только у готовых треков Spotify.
	Provider string `json:"provider,omitempty"`
	URI      string `json:"uri,omitempty"`
	TrackID  string `json:"track_id,omitempty"`
	CoverURL string `json:"cover_url,omitempty"`
}

func (t pendingTrack) line() string {
	if t.Artist == "" {
		return t.Title
	}
	return t.Artist + " — " + t.Title
}

// pendingPlaylist — плейлист, который ждёт решения.
type pendingPlaylist struct {
	// ID — короткий номер для команд и кнопок. Растёт от запуска к запуску,
	// в номера очереди не превращается: очередь двигается, а этот номер
	// должен пережить любое движение.
	ID string `json:"id"`

	Requester      string `json:"requester"`
	RequesterLogin string `json:"requester_login"`

	// Source — откуда плейлист, словами: «Spotify» или «Яндекс.Музыка».
	Source string `json:"source"`
	// Title — название плейлиста или альбома.
	Title string `json:"title"`
	URL   string `json:"url"`

	Tracks []pendingTrack `json:"tracks"`
	// Total — сколько треков было в плейлисте всего. Больше длины Tracks,
	// когда плейлист длиннее разрешённого.
	Total int `json:"total"`

	RedemptionID string    `json:"redemption_id"`
	RewardID     string    `json:"reward_id"`
	At           time.Time `json:"at"`

	// busy — плейлист прямо сейчас одобряют: треки ставятся в очередь.
	// Из ожидания его вынимают только после этого (см. approvePlaylist), а
	// пока он здесь, второе «одобрить» или «отклонить» его не трогает.
	busy bool
}

// TotalMs — сколько играть всю пачку.
func (p pendingPlaylist) TotalMs() int {
	sum := 0
	for _, t := range p.Tracks {
		sum += t.DurationMs
	}
	return sum
}

// ── хранение ─────────────────────────────────────────────────────────

// loadPlaylists читает ждущие плейлисты из базы. Зовётся при запуске.
func (s *Server) loadPlaylists() {
	raw, ok, err := s.db.GetKV(kvPlaylists)
	if err != nil {
		s.log.Warn("не прочитал ждущие плейлисты", "ошибка", err)
		return
	}
	if !ok || raw == "" {
		return
	}
	var list []pendingPlaylist
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		s.log.Warn("ждущие плейлисты не разобрались", "ошибка", err)
		return
	}
	s.mu.Lock()
	s.playlists = list
	// Счётчик номеров продолжаем с того места, где остановились. Иначе после
	// перезапуска новый плейлист получил бы номер уже занятый — и «одобрить 1»
	// решало бы судьбу не того заказа.
	for _, p := range list {
		if n, err := strconv.ParseInt(p.ID, 10, 64); err == nil && n > s.playlistNo {
			s.playlistNo = n
		}
	}
	s.mu.Unlock()
	if len(list) > 0 {
		s.log.Info("плейлисты ждут решения", "сколько", len(list))
	}
}

// savePlaylists пишет список целиком. Одна запись, которую всегда читают и
// пишут целиком, — отдельные строки тут ни к чему.
func (s *Server) savePlaylists(list []pendingPlaylist) {
	body, err := json.Marshal(list)
	if err != nil {
		s.log.Warn("не собрал ждущие плейлисты", "ошибка", err)
		return
	}
	if err := s.db.SetKV(kvPlaylists, string(body)); err != nil {
		s.log.Warn("не записал ждущие плейлисты", "ошибка", err)
	}
}

// pendingPlaylists отдаёт список ждущих.
func (s *Server) pendingPlaylists() []pendingPlaylist {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]pendingPlaylist, len(s.playlists))
	copy(out, s.playlists)
	return out
}

// addPending кладёт плейлист в очередь на одобрение.
func (s *Server) addPending(p pendingPlaylist) {
	s.mu.Lock()
	s.playlistNo++
	p.ID = strconv.FormatInt(s.playlistNo, 10)
	s.playlists = append(s.playlists, p)
	list := make([]pendingPlaylist, len(s.playlists))
	copy(list, s.playlists)
	s.mu.Unlock()

	s.savePlaylists(list)
	s.syncPlaylists()
}

// takePending забирает плейлист из ожидания.
//
// Пустой номер означает «единственный, если он один». Так удобнее в чате:
// когда ждёт один плейлист, номер писать незачем, а когда несколько —
// приложение попросит его назвать.
func (s *Server) takePending(id string) (pendingPlaylist, error) {
	s.mu.Lock()
	defer func() {
		list := make([]pendingPlaylist, len(s.playlists))
		copy(list, s.playlists)
		s.mu.Unlock()
		s.savePlaylists(list)
		s.syncPlaylists()
	}()

	if len(s.playlists) == 0 {
		return pendingPlaylist{}, errs.New(errs.PlaylistNone, "Сейчас ни один плейлист не ждёт решения.")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		if len(s.playlists) > 1 {
			return pendingPlaylist{}, errs.New(errs.PlaylistNone,
				"Решения ждёт больше одного плейлиста — скажи, какой именно, по номеру.")
		}
		id = s.playlists[0].ID
	}

	for i, p := range s.playlists {
		if p.ID == id {
			if p.busy {
				return pendingPlaylist{}, errs.New(errs.PlaylistNone, "Этот плейлист уже одобряют — треки ставятся в очередь.")
			}
			s.playlists = append(s.playlists[:i], s.playlists[i+1:]...)
			return p, nil
		}
	}
	return pendingPlaylist{}, errs.New(errs.PlaylistNone, "Плейлиста с таким номером в ожидании нет.")
}

// claimPending помечает плейлист «одобряется», не вынимая его из ожидания.
//
// Раньше одобрение сразу вынимало плейлист, а треки ставились потом, по
// одному, с поиском. Оборвись это посередине (перезапуск, сеть) — плейлиста
// нет ни в ожидании, ни целиком в очереди, а баллы списаны.
func (s *Server) claimPending(id string) (pendingPlaylist, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.playlists) == 0 {
		return pendingPlaylist{}, errs.New(errs.PlaylistNone, "Сейчас ни один плейлист не ждёт решения.")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		if len(s.playlists) > 1 {
			return pendingPlaylist{}, errs.New(errs.PlaylistNone,
				"Решения ждёт больше одного плейлиста — скажи, какой именно, по номеру.")
		}
		id = s.playlists[0].ID
	}
	for i := range s.playlists {
		if s.playlists[i].ID == id {
			if s.playlists[i].busy {
				return pendingPlaylist{}, errs.New(errs.PlaylistNone, "Этот плейлист уже одобряют — треки ставятся в очередь.")
			}
			s.playlists[i].busy = true
			return s.playlists[i], nil
		}
	}
	return pendingPlaylist{}, errs.New(errs.PlaylistNone, "Плейлиста с таким номером в ожидании нет.")
}

// releasePending снимает пометку «одобряется»: с удалением из ожидания или,
// если ничего не поставилось, оставляя плейлист ждать дальше.
func (s *Server) releasePending(id string, remove bool) {
	s.mu.Lock()
	for i := range s.playlists {
		if s.playlists[i].ID == id {
			if remove {
				s.playlists = append(s.playlists[:i], s.playlists[i+1:]...)
			} else {
				s.playlists[i].busy = false
			}
			break
		}
	}
	list := make([]pendingPlaylist, len(s.playlists))
	copy(list, s.playlists)
	s.mu.Unlock()
	s.savePlaylists(list)
	s.syncPlaylists()
}

// syncPlaylists переносит список ждущих в панель.
func (s *Server) syncPlaylists() {
	list := s.pendingPlaylists()
	view := make([]app.PlaylistView, 0, len(list))
	for _, p := range list {
		tracks := make([]string, 0, len(p.Tracks))
		for _, t := range p.Tracks {
			tracks = append(tracks, t.line())
		}
		view = append(view, app.PlaylistView{
			ID:        p.ID,
			Requester: p.Requester,
			Source:    p.Source,
			Title:     p.Title,
			URL:       p.URL,
			Tracks:    tracks,
			Total:     p.Total,
			LengthMs:  p.TotalMs(),
			At:        p.At,
		})
	}
	s.state.SetPlaylists(view)
}

// ── приём заказа ─────────────────────────────────────────────────────

// resolvePlaylist принимает заказ, пришедший по награде за плейлист.
func (s *Server) resolvePlaylist(ctx context.Context, r twitch.Redemption) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cfg := s.cfg.Get()

	link, ok := links.Find(r.UserInput)
	if !ok || !link.IsCollection() {
		s.log.Info("в заказе плейлиста нет ссылки на плейлист",
			"зритель", r.UserLogin, "заказ", r.UserInput)
		s.state.SetOrderMatch(r.ID, app.OrderMatch{
			State: app.MatchFailed,
			Note:  i18n.T("Это не ссылка на плейлист"),
		})
		s.rejectRedemption(ctx, r,
			i18n.T("это награда за плейлист — нужна ссылка на плейлист или альбом Spotify либо Яндекс.Музыки"))
		return
	}

	// Место в списке ожидания не бесконечно.
	if len(s.pendingPlaylists()) >= playlistKeep {
		s.rejectRedemption(ctx, r,
			i18n.T("слишком много плейлистов ждут решения — попробуй позже"))
		return
	}

	want := cfg.PlaylistTrackCount()
	col, source, err := s.readCollection(ctx, link, want)
	if err != nil {
		code, text := errs.Describe(err)
		s.log.Info("плейлист не прочитался",
			"зритель", r.UserLogin, "ссылка", link.URL, "код", code, "ошибка", err)
		s.state.SetOrderMatch(r.ID, app.OrderMatch{State: app.MatchFailed, Note: text})
		s.rejectRedemption(ctx, r, i18n.F("не получилось прочитать плейлист: %s", errText(err)))
		return
	}

	p := pendingPlaylist{
		Requester:      r.UserName,
		RequesterLogin: strings.ToLower(r.UserLogin),
		Source:         source,
		Title:          col.Title,
		URL:            link.URL,
		Tracks:         col.Tracks,
		Total:          col.Total,
		RedemptionID:   r.ID,
		RewardID:       r.RewardID,
		At:             time.Now(),
	}
	s.addPending(p)

	s.state.SetOrderMatch(r.ID, app.OrderMatch{
		State: app.MatchFound,
		Title: col.Title,
		Note:  i18n.T("Плейлист ждёт одобрения"),
	})
	s.log.Info("плейлист ждёт одобрения",
		"зритель", r.UserLogin, "плейлист", col.Title,
		"треков", len(col.Tracks), "всего_в_плейлисте", col.Total)

	// Зритель обязан понимать, что происходит: он заплатил, а в эфире пока
	// ничего. Без этой строки «оплатил и тишина» читается как поломка.
	part := ""
	if col.Total > len(col.Tracks) {
		part = i18n.F(" (первые %d из %d)", len(col.Tracks), col.Total)
	}
	s.say(ctx, i18n.F("@%s, плейлист «%s»%s принят и ждёт одобрения стримера. Не одобрят — баллы вернутся.",
		r.UserName, col.Title, part))
	s.state.Notify("info", i18n.F("Плейлист от %s: «%s» ждёт решения", r.UserName, col.Title))
}

// playlistCollection — плейлист, приведённый к общему виду.
type playlistCollection struct {
	Title  string
	Tracks []pendingTrack
	Total  int
}

// readCollection читает плейлист или альбом у того, кому он принадлежит.
func (s *Server) readCollection(ctx context.Context, link links.Link, want int) (playlistCollection, string, error) {
	switch link.Kind {
	case links.SpotifyPlaylist, links.SpotifyAlbum:
		if s.spotify == nil || !s.spotify.Connected() {
			return playlistCollection{}, "", errs.New(errs.SpotifyAuthExpired, "Spotify не подключён.")
		}
		if link.Kind == links.SpotifyPlaylist {
			got, err := s.spotify.PlaylistCollection(ctx, link.ID, want)
			if err != nil {
				return playlistCollection{}, "", err
			}
			return fromSpotifyCollection(got), "Spotify", nil
		}
		got, err := s.spotify.AlbumCollection(ctx, link.ID, want)
		if err != nil {
			return playlistCollection{}, "", err
		}
		return fromSpotifyCollection(got), "Spotify", nil

	case links.YandexPlaylist:
		got, err := s.yandex.Playlist(ctx, link.Owner, link.ID, want)
		if err != nil {
			return playlistCollection{}, "", err
		}
		return fromYandexCollection(got), "Яндекс.Музыка", nil

	case links.YandexAlbum:
		got, err := s.yandex.Album(ctx, link.ID, want)
		if err != nil {
			return playlistCollection{}, "", err
		}
		return fromYandexCollection(got), "Яндекс.Музыка", nil
	}
	return playlistCollection{}, "", errs.New(errs.PlaylistNone, "Такие плейлисты приложение не читает.")
}

func fromSpotifyCollection(col spotify.Collection) playlistCollection {
	out := playlistCollection{Title: col.Title, Total: col.Total}
	for _, t := range col.Tracks {
		out.Tracks = append(out.Tracks, pendingTrack{
			Artist: t.Artist, Title: t.Title, DurationMs: t.DurationMs,
			Provider: "spotify", URI: t.URI, TrackID: t.ID, CoverURL: t.CoverURL,
		})
	}
	if out.Total < len(out.Tracks) {
		out.Total = len(out.Tracks)
	}
	return out
}

func fromYandexCollection(col links.Collection) playlistCollection {
	out := playlistCollection{Title: col.Title, Total: col.Total}
	for _, t := range col.Tracks {
		// Адреса нет нарочно: сыграть трек Яндекса приложение не умеет, и
		// при одобрении он будет найден в Spotify или на YouTube. Зато
		// артист, название и длительность точные, и подбор выйдет
		// несравнимо лучше, чем по строке из чата.
		out.Tracks = append(out.Tracks, pendingTrack{
			Artist: t.Artist, Title: t.Title, DurationMs: t.DurationMs,
			CoverURL: t.CoverURL,
		})
	}
	if out.Total < len(out.Tracks) {
		out.Total = len(out.Tracks)
	}
	return out
}

// ── решение ──────────────────────────────────────────────────────────

// approvePlaylist ставит плейлист в очередь.
func (s *Server) approvePlaylist(ctx context.Context, id, actor string) (pendingPlaylist, int, error) {
	p, err := s.claimPending(id)
	if err != nil {
		return pendingPlaylist{}, 0, err
	}

	cfg := s.cfg.Get()
	added := 0
	cut := false
	for _, t := range p.Tracks {
		if ctx.Err() != nil {
			// Срок вышел посреди плейлиста (поиск по Яндексу бывает долгим).
			cut = true
			break
		}
		item, ok := s.playlistItem(ctx, p, t, cfg)
		if !ok {
			s.log.Info("трек плейлиста не удалось поставить",
				"плейлист", p.Title, "трек", t.line())
			continue
		}
		// В очередь кладём напрямую, а не через enqueue.
		//
		// enqueue проверил бы «не больше трёх заказов на зрителя» и отбил бы
		// весь плейлист начиная с четвёртого трека — при том что плейлист
		// уже одобрен человеком, и это одобрение и есть проверка. Он же
		// написал бы в чат отдельную строку на каждый трек: десять сообщений
		// подряд от бота — это не ответ, а спам.
		if _, err := s.queue.Add(item, false); err != nil {
			s.log.Warn("не поставил трек плейлиста в очередь", "трек", t.line(), "ошибка", err)
			continue
		}
		added++
	}
	// Из ожидания вынимаем только теперь, когда треки уже в очереди.
	s.releasePending(p.ID, true)

	if cut {
		s.log.Warn("плейлист поставлен не целиком: вышел срок",
			"плейлист", p.Title, "поставлено", added, "всего", len(p.Tracks))
	}

	if added == 0 {
		// Одобрили, а играть нечего. Баллы возвращаем: зритель ни в чём не
		// виноват. Срок исходного запроса мог уже выйти — тогда и возврат
		// с ним не дойдёт, поэтому берём свой.
		rctx, rcancel := context.WithTimeout(s.baseContext(), 20*time.Second)
		defer rcancel()
		s.refundPlaylist(rctx, p, "из плейлиста не удалось сыграть ни один трек")
		s.modLog(actor, "одобрил плейлист, но играть нечего", p.Title)
		return p, 0, errs.New(errs.PlaylistEmpty,
			"Из этого плейлиста не удалось поставить ни один трек. Баллы вернул.")
	}

	s.modLog(actor, "одобрил плейлист", p.Title+" ("+strconv.Itoa(added)+" "+plural(added, "трек", "трека", "треков")+" от "+p.Requester+")")
	s.state.Notify("info", i18n.F("%s одобрил плейлист «%s»: %d %s в очереди", actor, p.Title, added, tracksWord(added)))

	// Заказ на Twitch отмечаем выполненным: плейлист сыграет, баллы
	// потрачены не зря. Иначе он навсегда останется висеть в очереди наград.
	if p.RedemptionID != "" {
		// Свой срок: исходный мог выйти, пока ставились треки.
		fctx, fcancel := context.WithTimeout(s.baseContext(), 20*time.Second)
		err := s.twitch.FulfillRedemption(fctx, p.RewardID, p.RedemptionID)
		fcancel()
		if err != nil {
			s.log.Warn("не отметил плейлист выполненным", "ошибка", err)
		} else {
			s.state.SetRedemptionStatus(p.RedemptionID, app.OrderFulfilled)
		}
	}

	s.syncPlayback()
	s.player.Nudge()
	return p, added, nil
}

// playlistItem превращает трек плейлиста в заказ.
//
// У Spotify он уже готов. У Яндекса приходится искать — сначала в Spotify (там
// играть лучше: не нужен отдельный проигрыватель), потом на YouTube.
func (s *Server) playlistItem(ctx context.Context, p pendingPlaylist,
	t pendingTrack, cfg config.Config) (queue.Item, bool) {

	base := queue.Item{
		Source:         queue.SourcePlaylist,
		Requester:      p.Requester,
		RequesterLogin: p.RequesterLogin,
		RawRequest:     t.line(),
		Title:          t.Title,
		Artist:         t.Artist,
		DurationMs:     t.DurationMs,
		CoverURL:       t.CoverURL,
		Via:            i18n.F("из плейлиста «%s»", p.Title),
	}

	// Слишком длинное не ставим даже из одобренного плейлиста: часовой микс
	// посреди эфира — это не то, на что стример соглашался, нажимая
	// «одобрить». Настройка та же, что и у обычных заказов.
	tooLong := cfg.MaxTrackSeconds > 0 && t.DurationMs > cfg.MaxTrackSeconds*1000
	if tooLong {
		s.log.Info("трек плейлиста слишком длинный", "трек", t.line(), "длительность_мс", t.DurationMs)
		return queue.Item{}, false
	}

	if t.URI != "" {
		base.Provider = "spotify"
		base.URI = t.URI
		base.TrackID = t.TrackID
		base.Via = ""
		return base, true
	}

	// Ищем в Spotify по точным артисту и названию с известной длительностью —
	// это лучший запрос из возможных, куда точнее строки из чата.
	if s.spotify != nil && s.spotify.Connected() {
		req := match.Parse(t.Artist + " - " + t.Title)
		opts := matchOptionsFromConfig(cfg)
		opts.WantMs = t.DurationMs
		if me := s.spotify.Account(); me != nil {
			opts.Market = me.Country
		}
		if res, err := match.Find(ctx, s.spotify, req, opts); err == nil && res.Found {
			base.Provider = "spotify"
			base.URI = res.Track.URI
			base.TrackID = res.Track.ID
			base.Title = res.Track.Title
			base.Artist = firstArtist(res.Track.Artists)
			base.DurationMs = res.Track.DurationMs
			if res.Track.CoverURL != "" {
				base.CoverURL = res.Track.CoverURL
			}
			base.Via = ""
			return base, true
		}
	}

	// В Spotify нет — играем с YouTube, как обычный заказ.
	if found := s.findOnYouTube(ctx, "", strings.TrimSpace(t.Artist+" "+t.Title)); found != nil {
		found.Source = base.Source
		found.Requester = base.Requester
		found.RequesterLogin = base.RequesterLogin
		found.RawRequest = base.RawRequest
		found.Via = viaMissing
		return *found, true
	}

	return queue.Item{}, false
}

// rejectPlaylist отказывает в плейлисте и возвращает баллы.
func (s *Server) rejectPlaylist(ctx context.Context, id, actor string) (pendingPlaylist, error) {
	p, err := s.takePending(id)
	if err != nil {
		return pendingPlaylist{}, err
	}

	s.refundPlaylist(ctx, p, "плейлист не одобрили")
	s.modLog(actor, "отклонил плейлист", p.Title+" от "+p.Requester)
	s.state.Notify("info", i18n.F("%s отклонил плейлист «%s», баллы вернулись", actor, p.Title))
	return p, nil
}

// refundPlaylist возвращает баллы за плейлист.
func (s *Server) refundPlaylist(ctx context.Context, p pendingPlaylist, why string) {
	if p.RedemptionID == "" {
		return
	}
	if err := s.twitch.RefundRedemption(ctx, p.RewardID, p.RedemptionID); err != nil {
		s.log.Error("не смог вернуть баллы за плейлист",
			"зритель", p.Requester, "причина", why, "ошибка", err)
		s.state.NotifyError(err)
		return
	}
	s.log.Info("баллы за плейлист возвращены", "зритель", p.Requester, "причина", why)
	s.state.SetRedemptionStatus(p.RedemptionID, app.OrderRefunded)
}

// playlistRewardHint — что ответить зрителю, который кинул плейлист в награду
// за трек.
//
// Ответ разный, и это важно. Если награда за плейлист включена — говорим её
// название и цену, чтобы человек нажал правильную кнопку со второй попытки.
// Если выключена — честно говорим, что плейлисты тут не заказывают: обещать
// награду, которой нет на канале, значит отправить зрителя её искать.
func (s *Server) playlistRewardHint() string {
	cfg := s.cfg.Get()
	if !cfg.PlaylistReward || s.playlistReward() == "" {
		return i18n.T("плейлисты на этом канале не заказывают — кинь ссылку на один трек")
	}
	return i18n.F("это плейлист, а не трек — для него есть награда «%s» за %d %s",
		cfg.PlaylistRewardTitle, cfg.PlaylistPrice(),
		i18n.Plural(cfg.PlaylistPrice(), [3]string{"балл", "балла", "баллов"}, [2]string{"point", "points"}))
}
