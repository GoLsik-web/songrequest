package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// handleWS держит соединение с панелью. Данные уходят только когда состояние
// действительно изменилось: подписка отдаёт сигнал, мы шлём снимок. Никаких
// таймеров опроса — в простое сокет молчит, а процесс спит на канале.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Проверку Origin делаем сами в guard по Host, поэтому здесь она лишняя:
		// панель открывается и как 127.0.0.1, и как localhost.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx := conn.CloseRead(r.Context()) // входящие сообщения нам не нужны

	changed, unsubscribe := s.state.Subscribe()
	defer unsubscribe()

	send := func() error {
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return wsjson.Write(writeCtx, conn, s.state.Snapshot())
	}

	// Первый снимок — сразу, чтобы панель нарисовалась без ожидания события.
	if err := send(); err != nil {
		return
	}

	// Пинг раз в 30 секунд ловит случай, когда вкладку закрыли, а TCP-соединение
	// осталось висеть. Это единственный периодический таймер во всём приложении.
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case _, ok := <-changed:
			if !ok {
				return
			}
			if err := send(); err != nil {
				return
			}

		case <-ping.C:
			pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					s.log.Debug("панель отключилась")
				}
				return
			}
		}
	}
}
