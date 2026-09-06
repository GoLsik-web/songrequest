package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"songrequest/internal/app"
	"songrequest/internal/config"
	"songrequest/internal/diag"
	"songrequest/internal/errs"
	"songrequest/internal/spotify"
)

// redirectURI собирается из адреса, который мы реально слушаем. Он обязан
// совпадать с тем, что вписан в настройках приложения на developer.spotify.com,
// символ в символ — это причина примерно половины неудачных первых запусков.
func (s *Server) redirectURI() string { return s.addr + "/callback" }

func (s *Server) handleSpotifyLogin(w http.ResponseWriter, r *http.Request) {
	url, err := s.spotify.AuthURL(s.redirectURI())
	if err != nil {
		s.fail(w, err)
		return
	}
	// Пока обход работает, вход открываем в своём окне: браузер про обход не
	// знает, и страница входа Spotify из России у него просто не откроется.
	// Раньше на этом месте была просьба «включи VPN на время первого входа» —
	// то есть человеку всё равно нужна была отдельная программа обхода, ради
	// избавления от которой всё и делалось.
	if open := s.openAuth.Load(); open != nil && s.tunnel != nil && s.tunnel.Status().On {
		if err := (*open)(url); err != nil {
			s.log.Warn("не смог открыть вход в своём окне", "ошибка", err)
		} else {
			writeJSON(w, map[string]any{
				"url": url, "redirect_uri": s.redirectURI(), "in_app": true})
			return
		}
	}
	writeJSON(w, map[string]any{"url": url, "redirect_uri": s.redirectURI()})
}

// handleSpotifyCallback принимает ответ Spotify после входа. Открывается в
// браузере стримера, поэтому отвечает страницей, а не JSON.
func (s *Server) handleSpotifyCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	if reason := q.Get("error"); reason != "" {
		s.log.Warn("вход в Spotify отклонён", "причина", reason)
		s.state.NotifyCode("error", string(errs.SpotifyAuthDenied),
			"Вход в Spotify не завершён: доступ не выдан.")
		s.callbackPage(w, "Доступ не выдан", "Вернись в панель и попробуй ещё раз.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if err := s.spotify.Complete(ctx, q.Get("code"), q.Get("state")); err != nil {
		s.state.NotifyError(err)
		code, text := errs.Describe(err)
		s.log.Error("не завершил вход в Spotify", "код", code, "ошибка", err)
		s.callbackPage(w, "Не получилось", text)
		return
	}

	me, err := s.spotify.CheckAccount(ctx)
	if err != nil {
		s.state.NotifyError(err)
		s.syncSpotifyInfo()
		_, text := errs.Describe(err)
		s.callbackPage(w, "Вход выполнен, но есть проблема", text)
		return
	}

	s.syncSpotifyInfo()
	s.state.Notify("info", "Spotify подключён: "+me.DisplayName+" · "+me.PlanLabel())

	if problem := s.spotify.PlanProblem(me); problem != nil {
		s.reportPlanProblem(problem)
		s.callbackPage(w, "Вход выполнен", problem.Message)
		return
	}
	s.callbackPage(w, "Готово", "Spotify подключён. Возвращайся в панель — эта страница больше не нужна.")
}

func (s *Server) handleSpotifyLogout(w http.ResponseWriter, r *http.Request) {
	s.spotify.Logout()
	s.syncSpotifyInfo()
	s.state.Notify("info", "Spotify отключён")
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleSpotifyCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Человек нажал кнопку сам — значит, объявленную Spotify паузу он видел и
	// решил попробовать ещё раз. Запирать его до конца паузы нельзя: он ничего
	// другого сделать и не может. Один запрос по нажатию ограничение не
	// продлевает — продлевает поток запросов, а его держит пауза.
	s.spotify.ClearPause()

	me, err := s.spotify.CheckAccount(ctx)
	s.syncSpotifyInfo()
	if err != nil {
		s.noteSpotifyError(err)
		s.state.NotifyError(err)
		s.fail(w, err)
		return
	}

	s.state.Notify("info", "Spotify на связи: "+me.DisplayName+" · "+me.PlanLabel())
	if problem := s.spotify.PlanProblem(me); problem != nil {
		s.reportPlanProblem(problem)
	}
	writeJSON(w, me)
}

// handleSpotifyProbe перебирает способы спросить у Spotify поиск и содержимое
// плейлиста, а всё, что он ответил, кладёт в лог.
//
// Кнопка «Проверить поиск» в панели. Нужна одному человеку и один раз: у
// тестера поиск трека отвечает отказом «Invalid limit», хотя поиск артиста и
// всё остальное на том же аккаунте работает. Причина снаружи приложения, и
// увидеть её можно только живыми пробами на его машине.
func (s *Server) handleSpotifyProbe(w http.ResponseWriter, r *http.Request) {
	// Проб полтора десятка, каждая с походом в сеть: срок щедрый.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		query = "into you"
	}

	// Плейлист берём выбранный запасным, а нет такого — первый попавшийся:
	// пробы по нему объясняют жалобу «в плейлистах 0 треков».
	playlist := s.cfg.Get().FallbackPlaylistID
	if playlist == "" {
		if list, err := s.spotify.Playlists(ctx); err == nil && len(list) > 0 {
			playlist = list[0].ID
		}
	}

	probes := s.spotify.ProbeSearch(ctx, query, playlist)

	ok := 0
	for _, p := range probes {
		if p.OK() {
			ok++
		}
	}

	level := "info"
	if ok == 0 {
		level = "error"
	}
	s.state.Notify(level, fmt.Sprintf(
		"Проверка поиска: удачных проб %d из %d. Выгрузи лог и пришли его.", ok, len(probes)))

	writeJSON(w, map[string]any{"probes": probes, "ok": ok, "total": len(probes)})
}

// autoProbeSearch прогоняет пробы поиска сам, без кнопки, — один раз за
// запуск и только после того, как поиск уже отказал.
//
// Кнопка «Проверить поиск» появилась в 0.22.0 и не была нажата ни разу: два
// присланных лога подряд пришли без единой строки `проба поиска`, и разбор
// оба раза упёрся в «попроси нажать кнопку». Человека из этой цепочки надо
// убрать. Отказ поиска — сам по себе достаточный повод спросить Spotify
// пятнадцатью способами: строки лягут в тот же лог, который стример и так
// пришлёт, и следующий разбор начнётся с ответа, а не с просьбы.
//
// Один раз за запуск, потому что проб полтора десятка: повторять их на
// каждый заказ — это утопить лог и получить от Spotify паузу за частые
// запросы.
func (s *Server) autoProbeSearch(query string) {
	s.probeOnce.Do(func() {
		go func() {
			// Свой срок и свой контекст: заказ, из-за которого всё началось,
			// к этому времени давно закончится и свой контекст закроет.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			s.log.Warn("поиск отказал — проверяю поиск сам, кнопку ждать не будем",
				"запрос", query)
			s.spotify.ProbeSearch(ctx, query, s.cfg.Get().FallbackPlaylistID)
			s.state.Notify("error",
				"Поиск в Spotify отказал. Приложение проверило его само — нажми "+
					"«Сохранить лог и историю» и пришли архив.")
		}()
	})
}

// handleSpotifySnapshot снимает состояние плеера. На следующих этапах это
// будет делать очередь перед первым заказом, сейчас — кнопка в панели.
func (s *Server) handleSpotifySnapshot(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	snap, err := s.spotify.Capture(ctx)
	if err != nil {
		s.state.NotifyError(err)
		s.fail(w, err)
		return
	}

	// Кладём и плееру: панель рисует его снимок, а кнопка «Вернуть как было»
	// работала со своим. Два снимка на одно понятие — гарантированное
	// расхождение: человек видит одно, кнопка делает другое.
	if s.player != nil {
		s.player.SetSnapshot(snap)
	}

	s.snapMu.Lock()
	s.snap = snap
	s.snapMu.Unlock()

	at := snap.CapturedAt
	s.state.UpdateSpotify(func(info *app.SpotifyInfo) {
		info.SnapshotText = snap.Describe()
		info.SnapshotAt = &at
	})
	s.state.Notify("info", "Запомнил: "+snap.Describe())
	writeJSON(w, snap)
}

// handleSpotifyRestore возвращает плеер в запомненное состояние.
func (s *Server) handleSpotifyRestore(w http.ResponseWriter, r *http.Request) {
	// Возвращаем туда, что показано в панели, — то есть к снимку плеера.
	// Свой остаётся запасным на случай, если плеера ещё нет.
	if s.player != nil {
		if fromPlayer := s.player.Snapshot(); fromPlayer != nil {
			s.snapMu.Lock()
			s.snap = fromPlayer
			s.snapMu.Unlock()
		}
	}

	s.snapMu.Lock()
	snap := s.snap
	s.snapMu.Unlock()

	if snap == nil {
		err := errs.New(errs.SpotifyNothing, "Сначала нажми «Запомнить состояние».")
		s.state.NotifyError(err)
		s.fail(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Кнопку нажал человек — значит возвращаем даже если он только что
	// переключил трек руками. Защита от «стример взял управление на себя»
	// нужна автоматическому возврату после очереди, а не явной команде.
	outcome, err := s.spotify.Restore(ctx, snap, "", true)
	if err != nil {
		s.state.NotifyError(err)
		// Вернуть не вышло совсем — включаем то, что стример выбрал в настройках.
		s.startFresh(ctx, s.cfg.Get(), s.cfg.Get().EffectiveResumeMode(), snap)
		s.fail(w, err)
		return
	}

	if outcome.Restored {
		s.state.Notify("info", outcome.Message)
		// Снимок отработал по кнопке — плееру держать его больше незачем.
		// Иначе после следующей очереди он вернёт музыку туда же второй раз,
		// хотя стример уже давно слушает совсем другое.
		if s.player != nil {
			s.player.SetSnapshot(nil)
		}
	} else {
		s.state.NotifyCode("warn", string(outcome.Code), outcome.Message)
	}
	s.afterRestore(ctx, snap, outcome)
	writeJSON(w, outcome)
}

// handleProxyDetect ищет прокси на этом компьютере и сразу включает его.
//
// Смысл кнопки в том, чтобы стример не искал ничего сам: он запускает свою
// программу для обхода блокировок и нажимает сюда.
func (s *Server) handleProxyDetect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()

	// Сначала проверяем, нужен ли прокси вообще: если до Spotify доходит и
	// напрямую, лишний посредник только замедлит.
	if s.spotify.DirectWorks(ctx) {
		// Раньше здесь только говорилось «прокси не нужен», а сам прокси
		// оставался в настройках: панель чистила поле, мигала «сохранено», и
		// запросы продолжали идти через посредника. Убираем по-настоящему.
		if s.cfg.Get().SpotifyProxy != "" {
			if err := s.cfg.Update(func(c *config.Config) { c.SpotifyProxy = "" }); err != nil {
				s.fail(w, err)
				return
			}
			s.applyProxy()
		}
		s.state.Notify("info", "Прокси не нужен — до Spotify доходит напрямую.")
		writeJSON(w, map[string]string{"address": "", "note": "прямое соединение работает"})
		return
	}

	found, err := s.spotify.DetectProxy(ctx)
	if err != nil {
		s.state.NotifyError(err)
		s.fail(w, err)
		return
	}

	if err := s.cfg.Update(func(c *config.Config) { c.SpotifyProxy = found.Address }); err != nil {
		s.fail(w, err)
		return
	}
	s.applyProxy()
	s.state.Notify("info", "Прокси найден и включён: "+found.Address+" ("+found.Who+")")
	writeJSON(w, map[string]string{"address": found.Address, "who": found.Who})
}

// applyProxy включает или снимает посредника для Spotify.
//
// Вызывается при запуске и после каждого сохранения настроек: прокси должен
// начинать работать сразу, а не после перезапуска приложения — иначе человек
// решит, что он не работает вовсе.
//
// Обход внутри приложения главнее ручного поля «Прокси для Spotify». Двух
// посредников подряд быть не может, а выбирать за человека приходится: если
// он включил обход, значит ручной прокси ему как раз не помог.
func (s *Server) applyProxy() {
	if s.tunnel != nil {
		if st := s.tunnel.Status(); st.On {
			label := "обход внутри приложения"
			if st.Server != "" {
				label += " · " + st.Server
			}
			if err := s.spotify.SetTunnel(st.Addr, label); err != nil {
				s.log.Error("не смог направить Spotify через обход", "ошибка", err)
			} else {
				s.syncSpotifyInfo()
				return
			}
		}
	}

	shown, err := s.spotify.SetProxy(s.cfg.Get().SpotifyProxy)
	if err != nil {
		s.log.Error("прокси для Spotify не настроен", "ошибка", err)
		s.state.NotifyError(err)
		return
	}
	_ = shown // карточку соберёт syncSpotifyInfo, он и возьмёт метку у клиента
	s.syncSpotifyInfo()
}

func (s *Server) syncSpotifyInfo() {
	me := s.spotify.Account()
	info := app.SpotifyInfo{
		Connected:   s.spotify.Connected(),
		HasClientID: strings.TrimSpace(s.cfg.Get().SpotifyClientID) != "",
		Plan:        string(spotify.PlanUnknown),
		Proxy:       s.spotify.ProxyLabel(),
	}

	if left, part := s.spotify.PauseInfo(); left > 0 {
		until := time.Now().Add(left)
		info.PausedUntil = &until
		info.PausedPart = part
	}
	// Признак «пауза показана» держим здесь, рядом с самим показом.
	//
	// Раньше его переключал только notePause, и стоило карточке пересобраться
	// из другого места (проверка связи, вход, правка настроек), как признак
	// расходился с тем, что в панели: notePause считал, что пауза уже
	// нарисована, и молчал, а в карточке её не было. Живьём это выглядело как
	// «всё сломалось без объяснений».
	s.paused.Store(info.PausedUntil != nil)

	if me != nil {
		info.Account = me.DisplayName
		info.Email = me.Email
		info.Country = me.Country
		info.CountryNote = spotify.MarketProblem(me.Country)
		info.Plan = string(me.Plan())
		info.PlanLabel = me.PlanLabel()

		if problem := s.spotify.PlanProblem(me); problem != nil {
			info.PlanNote = problem.Message
			info.PlanNoteCode = string(problem.Code)
		}
	}

	s.snapMu.Lock()
	snap := s.snap
	s.snapMu.Unlock()
	// Снимок плеера главнее: он и есть тот, куда музыка вернётся сама.
	if s.player != nil {
		if fromPlayer := s.player.Snapshot(); fromPlayer != nil {
			snap = fromPlayer
		}
	}
	if snap != nil {
		at := snap.CapturedAt
		info.SnapshotText = snap.Describe()
		info.SnapshotAt = &at
	}

	s.state.SetSpotify(info)

	// Лампочка гаснет только тогда, когда работать точно нельзя. Неизвестная
	// подписка — повод предупредить, а не повод объявить всё сломанным.
	switch {
	case !info.HasClientID:
		s.state.SetConnIdle("Spotify", "Не настроено")
	case !info.Connected:
		s.state.SetConnIdle("Spotify", "Не подключён")
	case me == nil:
		// Красная лампочка без кода — тупик: человек может сказать мне только
		// «не работает». Поэтому называем настоящую причину, а пока проверка
		// ещё идёт, вообще не пугаем красным.
		if done, err := s.spotify.LastCheck(); done && err != nil {
			code, text := errs.Describe(err)
			s.state.SetConnFail("Spotify", string(code)+" · "+text)
		} else if done {
			s.state.SetConnFail("Spotify", "Вход есть, а данных нет. Нажми «Проверить связь».")
		} else {
			s.state.SetConnIdle("Spotify", "Проверяю связь…")
		}
	case me.Plan() == spotify.PlanFree:
		s.state.SetConnFail("Spotify", string(errs.SpotifyNoPremium)+" · нет Premium")
	case me.Plan() == spotify.PlanUnknown:
		s.state.SetConnOK("Spotify", me.DisplayName+" · подписка не определена")
	default:
		s.state.SetConnOK("Spotify", me.DisplayName+" · Premium")
	}
}

// reportPlanProblem показывает вопрос с подпиской в панели. Отсутствие
// Premium — ошибка, неопределённая подписка — предупреждение: работать при
// ней можно, и мешать человеку из-за неё нельзя.
func (s *Server) reportPlanProblem(problem *errs.Error) {
	if problem.Code == errs.SpotifyNoPremium {
		s.state.NotifyError(problem)
		return
	}
	s.state.NotifyWarn(problem.Code, problem.Message)
}

// handleSpotifyPlaylists отдаёт плейлисты стримера для выпадающего списка.
func (s *Server) handleSpotifyPlaylists(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	list, err := s.spotify.Playlists(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, list)
}

// handleDiagExport отдаёт zip с логом и настройками без секретов.
func (s *Server) handleDiagExport(w http.ResponseWriter, r *http.Request) {
	snapshot := s.state.Snapshot()
	conns := map[string]string{}
	for _, c := range snapshot.Connections {
		status := "не подключено"
		if c.Connected {
			status = "подключено"
		}
		if c.Detail != "" {
			status += " (" + c.Detail + ")"
		}
		conns[c.Name] = status
	}

	data, name, err := diag.Build(s.dataDir, s.cfg.Path(), s.log.Redactor, diag.Info{
		Version:     s.version,
		Connections: conns,
	}, s.diagTables())
	if err != nil {
		s.state.NotifyError(err)
		s.fail(w, err)
		return
	}

	s.log.Info("выгрузил архив с логом", "размер_байт", len(data))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	w.Write(data)
}

// fail отвечает панели кодом и понятным текстом.
func (s *Server) fail(w http.ResponseWriter, err error) {
	// Настоящую ошибку пишем в лог обязательно.
	//
	// Раньше сюда приходила ошибка без кода («не получилось очистить
	// очередь»), и наружу уходил только этот текст: ни кода, чтобы друг
	// назвал его голосом, ни строки в логе, чтобы разобрать потом. Причина —
	// заблокированная база, повреждённый файл — исчезала бесследно.
	code, text := errs.Describe(err)
	s.log.Error("отказ панели", "код", code, "текст", text, "ошибка", err)

	// Через эту воронку проходят все отказы панели — значит здесь же удобнее
	// всего заметить те, что говорят о дороге до Spotify.
	//
	// Живьём 31.08: приложение решило, что обход не нужен (Spotify отвечал), а
	// список плейлистов тут же получил «SP-16 Spotify не работает из этой
	// страны». Разбор дороги висел только на опросе плеера и на кнопке
	// «Проверить связь», и такой отказ проходил мимо него.
	s.noteSpotifyError(err)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	writeJSON(w, map[string]string{"code": string(code), "error": text})
}

func (s *Server) callbackPage(w http.ResponseWriter, title, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="ru"><head><meta charset="utf-8">
<title>%s</title><style>
body{background:#0e1013;color:#e7eaf0;font:16px/1.5 "Segoe UI",system-ui,sans-serif;
display:flex;align-items:center;justify-content:center;height:100vh;margin:0;text-align:center}
div{max-width:520px;padding:0 24px} h1{font-size:22px;margin:0 0 12px}
p{color:#8b93a1;margin:0}</style></head><body><div><h1>%s</h1><p>%s</p></div></body></html>`,
		title, title, text)
}

// SyncSpotify обновляет карточку Spotify в панели снаружи (при старте).
func (s *Server) SyncSpotify() { s.syncSpotifyInfo() }

// WarnIfPortChanged предупреждает, если панель поднялась не на том порту,
// который вписан в настройках приложения Spotify. Иначе вход будет падать с
// невнятным «invalid_grant», а причина — один отличающийся символ в адресе.
func (s *Server) WarnIfPortChanged() {
	want := s.cfg.Get().Port
	if want == 0 {
		return
	}
	_, actual, err := net.SplitHostPort(s.ln.Addr().String())
	if err != nil || actual == strconv.Itoa(want) {
		return
	}

	text := fmt.Sprintf(
		"Порт %d был занят, панель работает на %s. В настройках приложения Spotify адрес для возврата должен быть ровно %s",
		want, actual, s.redirectURI())
	s.log.Warn("панель на другом порту", "ожидали", want, "получили", actual)
	s.state.NotifyWarn(errs.SpotifyAuthDenied, text)
}
