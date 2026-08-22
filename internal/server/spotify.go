package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"songrequest/internal/app"
	"songrequest/internal/diag"
	"songrequest/internal/errs"
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
	writeJSON(w, map[string]string{"url": url, "redirect_uri": s.redirectURI()})
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
	s.state.Notify("info", "Spotify подключён: "+me.DisplayName)
	s.callbackPage(w, "Готово", "Spotify подключён. Эту вкладку можно закрыть и вернуться в панель.")
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

	me, err := s.spotify.CheckAccount(ctx)
	s.syncSpotifyInfo()
	if err != nil {
		s.state.NotifyError(err)
		s.fail(w, err)
		return
	}
	s.state.Notify("info", "Spotify на связи: "+me.DisplayName)
	writeJSON(w, me)
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

	// playedURI пустой: на этом этапе заказов ещё нет, мы просто проверяем
	// сам возврат. С появлением очереди сюда поедет трек, который играл заказ.
	outcome, err := s.spotify.Restore(ctx, snap, "")
	if err != nil {
		s.state.NotifyError(err)
		// Вернуть не вышло совсем — включаем то, что стример выбрал в настройках.
		s.startFresh(ctx, s.cfg.Get(), s.cfg.Get().EffectiveResumeMode(), snap)
		s.fail(w, err)
		return
	}

	if outcome.Restored {
		s.state.Notify("info", outcome.Message)
	} else {
		s.state.NotifyCode("warn", string(outcome.Code), outcome.Message)
	}
	s.afterRestore(ctx, snap, outcome)
	writeJSON(w, outcome)
}

func (s *Server) syncSpotifyInfo() {
	me := s.spotify.Account()
	info := app.SpotifyInfo{Connected: s.spotify.Connected()}
	if me != nil {
		info.Account = me.DisplayName
		info.Premium = me.Premium()
	}

	s.snapMu.Lock()
	snap := s.snap
	s.snapMu.Unlock()
	if snap != nil {
		at := snap.CapturedAt
		info.SnapshotText = snap.Describe()
		info.SnapshotAt = &at
	}

	s.state.SetSpotify(info)

	switch {
	case !info.Connected:
		s.state.SetConn("Spotify", false, "Не подключён")
	case me == nil:
		s.state.SetConn("Spotify", false, "Вход есть, но связи не было")
	case !info.Premium:
		s.state.SetConn("Spotify", false, string(errs.SpotifyNoPremium)+" · нет Premium")
	default:
		s.state.SetConn("Spotify", true, me.DisplayName)
	}
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
	})
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

// handleDebugLog включает подробный лог без перезапуска приложения.
func (s *Server) handleDebugLog(w http.ResponseWriter, r *http.Request) {
	on := r.URL.Query().Get("on") == "1"
	s.log.SetDebug(on)
	s.state.SetDebugLog(on)
	if on {
		s.log.Info("включён подробный лог")
		s.state.Notify("info", "Подробный лог включён. Повтори то, что не работало, и выгрузи лог.")
	} else {
		s.log.Info("подробный лог выключен")
	}
	writeJSON(w, map[string]bool{"debug": on})
}

// fail отвечает панели кодом и понятным текстом.
func (s *Server) fail(w http.ResponseWriter, err error) {
	code, text := errs.Describe(err)
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
