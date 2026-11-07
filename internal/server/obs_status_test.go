package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"songrequest/internal/config"
	"songrequest/internal/errs"
)

func TestOBSStatusRecoversAfterConnectionFailure(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		if wsjson.Write(ctx, c, map[string]any{"op": 0, "d": map[string]any{"rpcVersion": 1}}) != nil {
			return
		}
		var identify any
		if wsjson.Read(ctx, c, &identify) != nil {
			return
		}
		if wsjson.Write(ctx, c, map[string]any{"op": 2, "d": map[string]any{}}) != nil {
			return
		}
		var message any
		_ = wsjson.Read(ctx, c, &message)
	}))
	defer remote.Close()
	host, rawPort, _ := net.SplitHostPort(strings.TrimPrefix(remote.URL, "http://"))
	port, _ := strconv.Atoi(rawPort)
	s := commandServer(t)
	if err := s.cfg.Update(func(c *config.Config) { c.OBS.Enabled = true; c.OBS.Host = host; c.OBS.Port = port }); err != nil {
		t.Fatal(err)
	}
	s.obs.level, s.obs.code, s.obs.err = "fail", string(errs.OBSUnreachable), "old connection failure"
	s.obs.canvasW, s.obs.canvasH, s.obs.items = 1920, 1080, 2
	s.handleOBSStatus(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/obs", nil))
	got := s.obsStatus()
	if got["level"] != "ok" || got["code"] != "" || got["error"] != "" {
		t.Fatalf("stale connection failure: %#v", got)
	}
	if got["items"] != 2 || got["canvas"] != "1920×1080" {
		t.Fatal("health check changed source information")
	}
}
