package tunnel

import (
	"strings"
	"testing"
)

// HTTP-вход нужен mpv: без него звук с YouTube пошёл бы мимо обхода и
// YouTube ответил бы 403 (ссылка на поток привязана к адресу).
func TestBuildConfigHTTPInbound(t *testing.T) {
	s := Server{Kind: "vless", Host: "example.com", Port: 443, ID: "00000000-0000-0000-0000-000000000000", Net: "tcp"}
	cfg, err := buildConfig(s, 1080, 1081)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), `"protocol":"http"`) || !strings.Contains(string(cfg), `"port":1081`) {
		t.Fatalf("нет HTTP-входа: %s", cfg)
	}
	cfg, _ = buildConfig(s, 1080, 0)
	if strings.Contains(string(cfg), `"protocol":"http"`) {
		t.Fatal("HTTP-вход без порта")
	}
}
