package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"songrequest/internal/config"
	"songrequest/internal/errs"
	"songrequest/internal/i18n"
	"songrequest/internal/obs"
)

// Связь с OBS: источник «Браузер» с виджетом сам становится размером
// выбранного формата кадра и растягивается на весь холст OBS.
//
// Соединение не держится: каждое нажатие открывает его заново. OBS может
// быть закрыт — тогда панель просто покажет OBS-01, а приложение работает.

const keyringOBS = "obs_password"

// obsState — последнее, что известно о связи с OBS. Живёт только в памяти.
type obsState struct {
	mu       sync.Mutex
	level    string // "off" | "ok" | "fail"
	code     string
	err      string
	canvasW  int
	canvasH  int
	items    int
	noSource bool
	password string // если хранилища паролей нет (тесты)
}

func (s *Server) obsPassword() string {
	if s.secrets != nil {
		var p string
		if err := s.secrets.GetJSON(keyringOBS, &p); err == nil {
			return p
		}
	}
	s.obs.mu.Lock()
	defer s.obs.mu.Unlock()
	return s.obs.password
}

// widgetPort — порт, на котором виджет открыт для OBS.
func (s *Server) widgetPort() int {
	if _, p, err := net.SplitHostPort(s.addr); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			return n
		}
	}
	return s.cfg.Get().Port
}

// obsRun подключается к OBS и выполняет действие; итог запоминает.
func (s *Server) obsRun(do func(ctx context.Context, c *obs.Client, port, w, h int) (obs.Result, error)) {
	cfg := s.cfg.Get()
	ctx, cancel := context.WithTimeout(context.Background(), obs.Timeout)
	defer cancel()
	w, h := config.FrameSize(cfg.Widget.Frame)

	res, err := func() (obs.Result, error) {
		c, err := obs.Dial(ctx, cfg.OBS.Host, cfg.OBS.Port, s.obsPassword())
		if err != nil {
			return obs.Result{}, err
		}
		defer c.Close()
		return do(ctx, c, s.widgetPort(), w, h)
	}()

	s.obs.mu.Lock()
	defer s.obs.mu.Unlock()
	if res.CanvasW > 0 {
		s.obs.canvasW, s.obs.canvasH = res.CanvasW, res.CanvasH
	}
	s.obs.items = res.Items
	s.obs.noSource = errs.CodeOf(err) == errs.OBSNoSource
	if err != nil {
		code, text := errs.Describe(err)
		s.log.Warn("связь с OBS", "код", code, "ошибка", err)
		s.obs.level, s.obs.code, s.obs.err = "fail", string(code), text
		// Источника нет — это не поломка связи: OBS-то ответил.
		if s.obs.noSource {
			s.obs.level = "ok"
		}
		return
	}
	s.obs.level, s.obs.code, s.obs.err = "ok", "", ""
}

func (s *Server) obsFit() {
	s.obsRun(func(ctx context.Context, c *obs.Client, port, w, h int) (obs.Result, error) {
		return obs.Fit(ctx, c, port, w, h)
	})
}

// obsStatus — то, что видит панель.
func (s *Server) obsStatus() map[string]any {
	cfg := s.cfg.Get()
	s.obs.mu.Lock()
	defer s.obs.mu.Unlock()
	level := s.obs.level
	if level == "" || !cfg.OBS.Enabled {
		level = "off"
	}
	out := map[string]any{
		"enabled":   cfg.OBS.Enabled,
		"port":      cfg.OBS.Port,
		"level":     level,
		"code":      s.obs.code,
		"error":     s.obs.err,
		"items":     s.obs.items,
		"no_source": s.obs.noSource,
	}
	if s.obs.canvasW > 0 {
		out["canvas"] = fmt.Sprintf("%d×%d", s.obs.canvasW, s.obs.canvasH)
		best := config.FrameFor(s.obs.canvasW, s.obs.canvasH)
		fw, fh := config.FrameSize(cfg.Widget.Frame)
		// Кадр не той формы — растянем всё равно, но подскажем формат.
		if best != cfg.Widget.Frame && !sameAspect(fw, fh, s.obs.canvasW, s.obs.canvasH) {
			out["suggest"] = best
		}
	}
	return out
}

func sameAspect(w1, h1, w2, h2 int) bool {
	a, b := float64(w1)/float64(h1), float64(w2)/float64(h2)
	d := a/b - 1
	return d < 0.02 && d > -0.02
}

func (s *Server) handleOBSStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.obsStatus())
}

// handleOBSConnect запоминает порт и пароль, включает связь и сразу
// подгоняет источник.
func (s *Server) handleOBSConnect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Port     int    `json:"port"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		http.Error(w, i18n.T("не разобрал настройки"), http.StatusBadRequest)
		return
	}
	if pw := strings.TrimSpace(body.Password); pw != "" {
		if s.secrets != nil {
			if err := s.secrets.PutJSON(keyringOBS, pw); err != nil {
				s.log.Error("пароль OBS", "ошибка", err)
			}
		}
		s.obs.mu.Lock()
		s.obs.password = pw
		s.obs.mu.Unlock()
	}
	if err := s.cfg.Update(func(c *config.Config) {
		c.OBS.Enabled = true
		if body.Port > 0 && body.Port < 65536 {
			c.OBS.Port = body.Port
		}
		if c.OBS.Host == "" {
			c.OBS.Host = "127.0.0.1"
		}
	}); err != nil {
		s.fail(w, err)
		return
	}
	s.obsFit()
	writeJSON(w, s.obsStatus())
}

func (s *Server) handleOBSFit(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Get().OBS.Enabled {
		writeJSON(w, s.obsStatus())
		return
	}
	s.obsFit()
	writeJSON(w, s.obsStatus())
}

func (s *Server) handleOBSAdd(w http.ResponseWriter, r *http.Request) {
	u := fmt.Sprintf("http://127.0.0.1:%d/widget", s.widgetPort())
	s.obsRun(func(ctx context.Context, c *obs.Client, port, w, h int) (obs.Result, error) {
		return obs.Add(ctx, c, i18n.T("Заказ музыки"), u, w, h)
	})
	writeJSON(w, s.obsStatus())
}
