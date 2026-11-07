package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"songrequest/internal/config"
	"songrequest/internal/errs"
	"songrequest/internal/obs"
)

// Стиль виджета под сцену OBS.
//
// Стример задаёт в панели: сцена «Начало» — стиль «Заставка», «Конец» —
// «Прощальный», остальные — «Основной». Приложение держит с OBS соединение
// на события сцен и при переключении надевает на плашку нужный стиль — так
// же, как щелчок по его плитке. Соединение есть, только пока это нужно:
// связь с OBS включена и хоть одна сцена назначена.

func (s *Server) sceneStylesWanted() bool {
	cfg := s.cfg.Get()
	return cfg.OBS.Enabled && len(cfg.Widget.SceneStyles) > 0
}

// applySceneStyle — сцена стала текущей: надеть её стиль (или общий "").
func (s *Server) applySceneStyle(scene string) {
	cfg := s.cfg.Get()
	id, ok := cfg.Widget.SceneStyles[scene]
	if !ok {
		id = cfg.Widget.SceneStyles[""]
	}
	if id == "" || id == cfg.Widget.Style {
		return
	}
	changed := false
	s.cfg.Update(func(c *config.Config) { changed = c.Widget.ApplyStyle(id) })
	if changed {
		s.state.SetWidget(s.cfg.Get().Widget)
		s.log.Info("стиль виджета по сцене", "сцена", scene, "стиль", id)
	}
}

func (s *Server) watchWidgetScenes(base context.Context) {
	wait := 3 * time.Second
	for base.Err() == nil {
		if !s.sceneStylesWanted() {
			sleepCtx(base, 5*time.Second)
			continue
		}
		cfg := s.cfg.Get().OBS
		ctx, cancel := context.WithTimeout(base, obs.Timeout)
		c, err := obs.DialEvents(ctx, cfg.Host, cfg.Port, s.obsPassword(), obs.EventsScenes)
		cancel()
		if err != nil {
			sleepCtx(base, wait)
			if wait < 30*time.Second {
				wait *= 2
			}
			continue
		}
		wait = 3 * time.Second
		s.pumpSceneStyles(base, c)
		c.Close()
	}
}

func (s *Server) pumpSceneStyles(base context.Context, ev *obs.Client) {
	// текущая сцена — сразу, не дожидаясь переключения
	ctx, cancel := context.WithTimeout(base, obs.Timeout)
	if c, err := obs.Dial(ctx, s.cfg.Get().OBS.Host, s.cfg.Get().OBS.Port, s.obsPassword()); err == nil {
		var cur struct {
			Name string `json:"currentProgramSceneName"`
		}
		if c.Call(ctx, "GetCurrentProgramScene", nil, &cur) == nil {
			s.applySceneStyle(cur.Name)
		}
		c.Close()
	}
	cancel()

	ctx, stop := context.WithCancel(base)
	defer stop()
	scenes := make(chan string, 4)
	go func() {
		defer close(scenes)
		for {
			typ, data, err := ev.NextEvent(ctx)
			if err != nil {
				return
			}
			if typ != "CurrentProgramSceneChanged" {
				continue
			}
			var e struct {
				Name string `json:"sceneName"`
			}
			if json.Unmarshal(data, &e) == nil {
				select {
				case scenes <- e.Name:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	check := time.NewTicker(5 * time.Second)
	defer check.Stop()
	for {
		select {
		case name, ok := <-scenes:
			if !ok {
				return
			}
			s.applySceneStyle(name)
		case <-check.C:
			if !s.sceneStylesWanted() {
				return
			}
		case <-base.Done():
			return
		}
	}
}

// handleOBSScenes — список сцен OBS для панели (назначить стили).
func (s *Server) handleOBSScenes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), obs.Timeout)
	defer cancel()
	out := struct {
		Scenes  []string `json:"scenes"`
		Current string   `json:"current"`
		Error   string   `json:"error,omitempty"`
	}{Scenes: []string{}}
	c, err := obs.Dial(ctx, s.cfg.Get().OBS.Host, s.cfg.Get().OBS.Port, s.obsPassword())
	if err != nil {
		_, out.Error = errs.Describe(err)
		writeJSON(w, out)
		return
	}
	defer c.Close()
	var list struct {
		Current string `json:"currentProgramSceneName"`
		Scenes  []struct {
			Name string `json:"sceneName"`
		} `json:"scenes"`
	}
	if err := c.Call(ctx, "GetSceneList", nil, &list); err != nil {
		_, out.Error = errs.Describe(err)
		writeJSON(w, out)
		return
	}
	// OBS отдаёт сцены снизу вверх — переворачиваем, как в его списке
	for i := len(list.Scenes) - 1; i >= 0; i-- {
		out.Scenes = append(out.Scenes, list.Scenes[i].Name)
	}
	out.Current = list.Current
	writeJSON(w, out)
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
