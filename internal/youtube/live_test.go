package youtube

import (
	"context"
	"os"
	"testing"

	"songrequest/internal/errs"
	"songrequest/internal/logx"
)

// Живая проверка: настоящий yt-dlp на настоящем ролике. Запускается только
// когда явно попросили, чтобы обычный прогон тестов не ходил в сеть.
func TestLiveSearch(t *testing.T) {
	if os.Getenv("SONGREQUEST_LIVE") == "" {
		t.Skip("живая проверка выключена")
	}

	log, _ := logx.New(t.TempDir(), true)
	defer log.Close()

	tools := NewTools(log, t.TempDir())
	if err := tools.Ensure(context.Background(), "", ""); err != nil {
		t.Logf("инструменты не полны (это ожидаемо без mpv): %v", err)
	}
	if ytdlp, _ := tools.Paths(); ytdlp == "" {
		t.Fatal("yt-dlp не нашёлся и не скачался")
	}

	p := NewPlayer(log, tools)
	track, err := p.Search(context.Background(), "Queen Bohemian Rhapsody")
	if err != nil {
		// YouTube требует подтвердить, что мы не робот, и куки браузера на
		// этой машине недоступны. Это среда, а не ошибка кода.
		if errs.CodeOf(err) == errs.YouTubeCookies {
			t.Skipf("YouTube не отдал трек без куки браузера: %v", err)
		}
		t.Fatalf("поиск не удался: %v", err)
	}
	t.Logf("нашлось: %s — %s (%d мс) %s", track.Artist, track.Title, track.DurationMs, track.URL)

	if track.ID == "" || track.DurationMs == 0 {
		t.Fatalf("метаданные неполные: %+v", track)
	}
}
