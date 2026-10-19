package server

import (
	"context"
	"time"

	"songrequest/internal/config"
	"songrequest/internal/tunnel"
)

// Обход для YouTube.
//
// 12.10 у владельца с VPN на весь компьютер YouTube отказывал «подтверди, что
// ты не бот» и yt-dlp, и встроенному плееру в браузере: адреса VPN-серверов
// YouTube метит целиком. Через сервер из обхода приложения тот же ролик
// открылся сразу. Поэтому при таком отказе YouTube идёт через обход.
//
// Обход отдельный от Spotify. Общий нельзя: при своём VPN стримера обход для
// Spotify нарочно не поднимается (через два туннеля подряд Spotify отвечал
// по двадцать секунд), и поднять его ради YouTube значило бы увести туда и
// Spotify. Если же общий обход и так работает (стример без VPN в России), берём
// его: YouTube там без него ещё и тормозит.

// ytRoute отдаёт посредника для YouTube. raise — поднять, если его нет.
func (s *Server) ytRoute(ctx context.Context, raise bool) (socks, http string) {
	if s.tunnel != nil {
		if st := s.tunnel.Status(); st.On {
			return st.Addr, s.tunnel.HTTPAddr()
		}
	}
	s.ytTunnelMu.Lock()
	defer s.ytTunnelMu.Unlock()
	if s.ytTunnel != nil && s.ytTunnel.Alive() {
		return s.ytTunnel.Addr(), s.ytTunnel.HTTPAddr()
	}
	if !raise {
		return "", ""
	}
	return s.raiseYTTunnel(ctx)
}

// raiseYTTunnel поднимает обход для YouTube. Зовётся под ytTunnelMu.
func (s *Server) raiseYTTunnel(ctx context.Context) (socks, http string) {
	if s.secrets == nil {
		return "", ""
	}
	var saved savedKey
	if err := s.secrets.GetJSON(keyringTunnel, &saved); err != nil || saved.Key == "" {
		s.log.Info("YouTube не пускает, а ключа обхода нет — обойти нечем")
		return "", ""
	}
	if s.ytTunnel == nil {
		s.ytTunnel = tunnel.New(s.log, s.dataDir)
		s.ytTunnel.Probe = tunnel.CheckYouTube
		s.ytTunnel.SetCache(tunnelCache{secrets: s.secrets})
	}
	cfg := s.cfg.Get()
	// Срок короткий: заказ ждёт. Сервер из прошлого раза поднимается за пару
	// секунд, перебор всех — дольше, но это случается один раз.
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	s.log.Info("поднимаю обход для YouTube")
	st, err := s.ytTunnel.Start(ctx, saved.Key, cfg.YouTubeTunnelServer, cfg.TunnelToolPath)
	if err != nil {
		s.log.Warn("обход для YouTube не поднялся", "ошибка", err)
		return "", ""
	}
	if st.Server != cfg.YouTubeTunnelServer {
		_ = s.cfg.Update(func(c *config.Config) { c.YouTubeTunnelServer = st.Server })
	}
	s.log.Info("YouTube идёт через обход", "сервер", st.Server)
	return st.Addr, s.ytTunnel.HTTPAddr()
}

// ytRouteBad — через нынешний сервер YouTube тоже говорит «не бот»: помечаем
// его и гасим, следующий подъём возьмёт другой.
func (s *Server) ytRouteBad() {
	s.ytTunnelMu.Lock()
	defer s.ytTunnelMu.Unlock()
	if s.ytTunnel == nil {
		return
	}
	if st := s.ytTunnel.Status(); st.Server != "" {
		s.log.Warn("YouTube не пускает и через этот сервер обхода", "сервер", st.Server)
		s.ytTunnel.MarkBad(st.Server)
		_ = s.cfg.Update(func(c *config.Config) { c.YouTubeTunnelServer = "" })
	}
	s.ytTunnel.Stop()
}

// prewarmYTTunnel поднимает обход для YouTube при запуске, если в прошлый раз
// без него YouTube не пускал: иначе первый заказ ждал бы подъёма.
func (s *Server) prewarmYTTunnel(ctx context.Context) {
	if s.cfg.Get().YouTubeTunnelServer == "" {
		return
	}
	go func() {
		s.ytTunnelMu.Lock()
		defer s.ytTunnelMu.Unlock()
		s.raiseYTTunnel(ctx)
	}()
}

// stopYTTunnel гасит обход для YouTube при выходе.
func (s *Server) stopYTTunnel() {
	s.ytTunnelMu.Lock()
	defer s.ytTunnelMu.Unlock()
	if s.ytTunnel != nil {
		s.ytTunnel.Stop()
	}
}
