package server

import (
	"context"

	"songrequest/internal/diag"
)

// StartWatch запускает подробный журнал. Пока переключатель выключен,
// наблюдатель спит и ничего не пишет. См. internal/diag/watch.go.
func (s *Server) StartWatch(ctx context.Context) {
	go diag.Watch(ctx, s.log, diag.WatchOptions{
		On: func() bool { return s.cfg.Get().VerboseLog },
		TunnelAddr: func() string {
			if s.tunnel == nil {
				return ""
			}
			return s.tunnel.Addr()
		},
	})
}
