package spotify

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"songrequest/internal/config"
	"songrequest/internal/errs"
)

// Ровно та ошибка, из-за которой у тестера с оплаченным Premium приложение
// говорило «премиума нет»: поле product приходит только при выданном праве
// user-read-private, а мы его не запрашивали. Пустой product — это «Spotify
// не сказал», и путать это с «подписки нет» нельзя.

func TestPlanIsThreeStates(t *testing.T) {
	tests := []struct {
		name    string
		product string
		want    Plan
	}{
		{"премиум подтверждён", "premium", PlanPremium},
		{"семейный тариф — тоже премиум", "premium_family", PlanPremium},
		{"бесплатный аккаунт", "free", PlanFree},
		{"open — тоже без подписки", "open", PlanFree},
		{"поля нет — определить не удалось", "", PlanUnknown},
		{"пробелы вместо значения", "   ", PlanUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			me := &Me{Product: tc.product}
			if got := me.Plan(); got != tc.want {
				t.Fatalf("получили %q, ждали %q", got, tc.want)
			}
		})
	}
}

func TestAuthAsksForScopesNeededToReadPlan(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})
	c.cfg.Update(func(cf *config.Config) { cf.SpotifyClientID = "тестовый-client-id" })

	raw, err := c.AuthURL("http://127.0.0.1:8977/callback")
	if err != nil {
		t.Fatal(err)
	}

	// Без user-read-private Spotify не пришлёт product, и проверка подписки
	// сломается снова — поэтому право проверяется тестом, а не глазами.
	for _, want := range []string{"user-read-private", "user-read-email"} {
		if !strings.Contains(raw, want) {
			t.Errorf("в запросе прав не хватает %q", want)
		}
	}
}

// Главный случай: Premium есть, а Spotify про него молчит, потому что права
// не выданы. Работу это блокировать не должно.
func TestMissingProductDoesNotClaimNoPremium(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Ровно то, что приходит без user-read-private: product отсутствует.
		writeJSON(w, map[string]any{
			"id": "tester", "display_name": "Тестер", "type": "user",
		})
	})
	c.tokens.Scope = "user-read-playback-state user-modify-playback-state"

	me, err := c.CheckAccount(context.Background())
	if err != nil {
		t.Fatalf("отсутствие поля product — не сбой связи: %v", err)
	}
	if me.Plan() != PlanUnknown {
		t.Fatalf("ждали «не определили», получили %q", me.Plan())
	}

	problem := c.PlanProblem(me)
	if problem == nil {
		t.Fatal("о неопределённой подписке надо предупредить")
	}
	if problem.Code != errs.SpotifyPlanUnknown {
		t.Fatalf("ждали код %s, получили %s", errs.SpotifyPlanUnknown, problem.Code)
	}
	if problem.Code == errs.SpotifyNoPremium {
		t.Fatal("нельзя утверждать, что подписки нет, когда мы этого не знаем")
	}
	// Причина лечится одной кнопкой, поэтому текст обязан её называть.
	if !strings.Contains(problem.Message, "заново") {
		t.Fatalf("текст должен подсказать переподключение: %q", problem.Message)
	}
}

// Право выдано, а product всё равно пустой — это уже не наша вина, но и
// не повод объявлять человеку, что у него нет подписки.
func TestMissingProductWithScopeGrantedIsStillUnknown(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"id": "tester", "display_name": "Тестер"})
	})
	c.tokens.Scope = strings.Join(scopes, " ")

	me, _ := c.CheckAccount(context.Background())
	problem := c.PlanProblem(me)

	if problem.Code != errs.SpotifyPlanUnknown {
		t.Fatalf("ждали код %s, получили %s", errs.SpotifyPlanUnknown, problem.Code)
	}
	if strings.Contains(problem.Message, "заново") {
		t.Fatal("права уже выданы — переподключение тут не поможет и советовать его нельзя")
	}
}

func TestExplicitFreeAccountIsReportedAsNoPremium(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"id": "tester", "display_name": "Тестер", "product": "free",
		})
	})
	c.tokens.Scope = strings.Join(scopes, " ")

	me, _ := c.CheckAccount(context.Background())
	problem := c.PlanProblem(me)

	if problem == nil || problem.Code != errs.SpotifyNoPremium {
		t.Fatalf("ждали код %s, получили %v", errs.SpotifyNoPremium, problem)
	}
}

func TestPremiumAccountHasNoProblem(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"id": "tester", "display_name": "Тестер",
			"product": "premium", "email": "tester@example.com", "country": "RU",
		})
	})
	c.tokens.Scope = strings.Join(scopes, " ")

	me, err := c.CheckAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p := c.PlanProblem(me); p != nil {
		t.Fatalf("у премиума проблем быть не должно: %v", p)
	}
	if me.Email != "tester@example.com" {
		t.Fatalf("почта нужна панели, чтобы стример видел, каким аккаунтом вошёл: %q", me.Email)
	}
	if me.PlanLabel() != "Premium" {
		t.Fatalf("подпись подписки: %q", me.PlanLabel())
	}
}

// Ответ Spotify должен попадать в лог целиком: именно по нему разбирают
// «у меня Premium, а приложение говорит, что нет».
func TestAccountResponseIsLoggedWholeButWithoutEmail(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"id": "tester", "display_name": "Тестер",
			"email": "секретная-почта@example.com", "country": "RU",
		})
	})
	c.tokens.Scope = "user-read-playback-state"

	if _, err := c.CheckAccount(context.Background()); err != nil {
		t.Fatal(err)
	}

	logged := readLogFile(t, c.log.Path)

	if !strings.Contains(logged, "display_name") {
		t.Fatal("в логе должен быть полный ответ Spotify, иначе разбирать нечего")
	}
	if !strings.Contains(logged, "user-read-playback-state") {
		t.Fatal("в логе должны быть выданные права — без них причина непонятна")
	}
	if strings.Contains(logged, "секретная-почта@example.com") {
		t.Fatal("почта не должна уезжать в архиве с логом")
	}
}

func readLogFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("не прочитал лог: %v", err)
	}
	return string(data)
}
