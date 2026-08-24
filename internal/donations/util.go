package donations

import (
	"context"
	"math/rand"
	"time"
)

// backoff — пауза перед повтором с ростом и разбросом. Сервисы донатов
// падают целиком и разом, и одновременный штурм от всех приложений им
// не помогает.
func backoff(attempt int) time.Duration {
	if attempt > 6 {
		attempt = 6
	}
	base := time.Duration(1<<uint(attempt-1)) * time.Second
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	return base + time.Duration(rand.Int63n(int64(500*time.Millisecond)))
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
