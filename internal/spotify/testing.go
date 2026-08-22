package spotify

import (
	"strings"
	"time"

	"songrequest/internal/config"
	"songrequest/internal/logx"
)

// NewForTest создаёт клиент, который ходит на подставной сервер вместо Spotify
// и считает себя уже вошедшим. Лежит в обычном файле, а не в _test.go, потому
// что нужен тестам соседних пакетов.
func NewForTest(cfg *config.File, log *logx.Logger, sec SecretStore, baseURL string) *Client {
	c := New(cfg, log, sec)
	c.apiBase = baseURL
	c.tokenURL = baseURL + "/api/token"
	c.tokens = tokens{
		AccessToken:  "тестовый-ключ-доступа",
		RefreshToken: "тестовый-ключ-обновления",
		ExpiresAt:    time.Now().Add(time.Hour),
		Scope:        strings.Join(scopes, " "),
	}
	return c
}
