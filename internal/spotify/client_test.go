package spotify

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"songrequest/internal/config"
	"songrequest/internal/errs"
)

func TestRetriesServerErrorThenSucceeds(t *testing.T) {
	var hits int32

	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway) // у Spotify икота
			return
		}
		writeJSON(w, map[string]any{"display_name": "Стример", "product": "premium"})
	})

	me, err := c.CheckAccount(context.Background())
	if err != nil {
		t.Fatalf("один таймаут не должен ломать приложение: %v", err)
	}
	if me.DisplayName != "Стример" || !me.Premium() {
		t.Fatalf("аккаунт разобран неверно: %+v", me)
	}
	if hits != 2 {
		t.Fatalf("ждали одну повторную попытку, было запросов: %d", hits)
	}
}

func TestRespectsRetryAfterOnRateLimit(t *testing.T) {
	var hits int32
	start := time.Now()

	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("Retry-After", "0") // «подожди и приходи снова»
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		writeJSON(w, map[string]any{"display_name": "Стример", "product": "premium"})
	})

	if _, err := c.CheckAccount(context.Background()); err != nil {
		t.Fatalf("после паузы запрос должен пройти: %v", err)
	}
	// Retry-After: 0 плюс наша секунда сверху — ждём не меньше секунды.
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Fatalf("не выдержали паузу, которую попросил Spotify: %v", elapsed)
	}
}

func TestRefreshesTokenOnUnauthorized(t *testing.T) {
	var apiHits, tokenHits int32

	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/api/token") {
			atomic.AddInt32(&tokenHits, 1)
			writeJSON(w, map[string]any{
				"access_token": "свежий-ключ-доступа", "expires_in": 3600, "token_type": "Bearer",
			})
			return
		}
		if atomic.AddInt32(&apiHits, 1) == 1 {
			w.WriteHeader(http.StatusUnauthorized) // ключ протух раньше срока
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer свежий-ключ-доступа" {
			t.Errorf("после обновления пошли со старым ключом: %q", got)
		}
		writeJSON(w, map[string]any{"display_name": "Стример", "product": "premium"})
	})

	if _, err := c.CheckAccount(context.Background()); err != nil {
		t.Fatalf("протухший ключ должен обновляться сам: %v", err)
	}
	if tokenHits != 1 {
		t.Fatalf("ждали одно обновление ключа, было: %d", tokenHits)
	}
}

func TestLogoutWhenRefreshRejected(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/api/token") {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"invalid_grant","error_description":"Refresh token revoked"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})

	// Ключ доступа просрочен, значит клиент пойдёт обновляться и получит отказ.
	c.tokens.ExpiresAt = time.Now().Add(-time.Hour)

	_, err := c.CheckAccount(context.Background())
	if errs.CodeOf(err) != errs.SpotifyAuthExpired {
		t.Fatalf("ждали код %s, получили %v", errs.SpotifyAuthExpired, err)
	}
	if c.Connected() {
		t.Fatal("после отзыва доступа приложение не должно считать себя подключённым")
	}
}

func TestNoActiveDeviceIsTranslatedToHumanText(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"status":404,"message":"Player command failed: No active device found","reason":"NO_ACTIVE_DEVICE"}}`))
	})

	err := c.PlayTrack(context.Background(), "spotify:track:любой", "")
	if errs.CodeOf(err) != errs.SpotifyNoDevice {
		t.Fatalf("ждали код %s, получили %v", errs.SpotifyNoDevice, err)
	}

	var e *errs.Error
	errs.As(err, &e)
	if strings.Contains(e.Message, "404") || strings.Contains(e.Message, "NO_ACTIVE_DEVICE") {
		t.Fatalf("текст для стримера не должен быть техническим: %q", e.Message)
	}
}

func TestAuthURLRequiresClientID(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})

	_, err := c.AuthURL("http://127.0.0.1:8977/callback")
	if errs.CodeOf(err) != errs.NoClientID {
		t.Fatalf("ждали код %s, получили %v", errs.NoClientID, err)
	}
}

func TestAuthURLHasPKCEChallenge(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})
	c.cfg.Update(func(cf *config.Config) { cf.SpotifyClientID = "тестовый-client-id" })

	raw, err := c.AuthURL("http://127.0.0.1:8977/callback")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"code_challenge_method=S256",
		"code_challenge=",
		"response_type=code",
		"redirect_uri=http%3A%2F%2F127.0.0.1%3A8977%2Fcallback",
		"user-modify-playback-state",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("в ссылке входа не хватает %q", want)
		}
	}
	if strings.Contains(raw, "client_secret") {
		t.Error("в PKCE секрета быть не должно")
	}
}

func TestCompleteRejectsMismatchedState(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("до обмена кода дело дойти не должно")
	})
	c.cfg.Update(func(cf *config.Config) { cf.SpotifyClientID = "тестовый-client-id" })

	if _, err := c.AuthURL("http://127.0.0.1:8977/callback"); err != nil {
		t.Fatal(err)
	}

	err := c.Complete(context.Background(), "какой-то-код", "чужой-state")
	if errs.CodeOf(err) != errs.SpotifyAuthDenied {
		t.Fatalf("подменённый ответ должен отклоняться, получили %v", err)
	}
}
