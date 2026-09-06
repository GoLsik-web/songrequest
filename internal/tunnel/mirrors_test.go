package tunnel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"songrequest/internal/logx"
)

// Запасные пути к списку серверов.
//
// Сервис подписки — обычный сайт: он падает, его блокируют, у него кончается
// домен. Раньше в такой вечер обхода не было вовсе, хотя вчерашние серверы
// никуда не делись. Проверки ниже — про три способа это пережить: запасные
// ссылки, повтор при обрыве связи и список, запомненный в прошлый раз.

func testTunnel(t *testing.T) *Tunnel {
	t.Helper()
	log, err := logx.New(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	return New(log, t.TempDir())
}

// Один рабочий ключ, которого хватает для разбора.
const sampleKey = "vless://11111111-2222-3333-4444-555555555555@1.2.3.4:443?type=tcp&security=tls#Проба"

func TestSplitKeySeparatesLinksFromKeys(t *testing.T) {
	links, keys := SplitKey("https://one.example/sub https://two.example/sub\n" + sampleKey)

	if len(links) != 2 || links[0] != "https://one.example/sub" || links[1] != "https://two.example/sub" {
		t.Fatalf("ссылки разобрались не так: %#v", links)
	}
	if keys != sampleKey {
		t.Fatalf("ключ потерялся: %q", keys)
	}
}

// У ключа после решётки стоит имя сервера, а в имени пробелы — обычное дело.
// Делить такую строку по пробелам нельзя.
func TestSplitKeyKeepsSpacesInsideKeyNames(t *testing.T) {
	line := "vless://11111111-2222-3333-4444-555555555555@1.2.3.4:443?type=tcp&security=tls#Сервер в Албании"
	links, keys := SplitKey(line)
	if len(links) != 0 {
		t.Fatalf("ключ приняли за ссылку: %#v", links)
	}
	if keys != line {
		t.Fatalf("имя сервера порвалось: %q", keys)
	}
}

// Первое зеркало легло — берём следующее, а не сдаёмся.
func TestResolveFallsToNextMirror(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "нет такого", http.StatusNotFound)
	}))
	defer dead.Close()
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleKey))
	}))
	defer alive.Close()

	servers, cached, err := testTunnel(t).resolve(context.Background(), dead.URL+" "+alive.URL)
	if err != nil {
		t.Fatalf("второе зеркало не спасло: %v", err)
	}
	if cached {
		t.Fatal("список взят из памяти, хотя зеркало ответило")
	}
	if len(servers) != 1 || servers[0].Host != "1.2.3.4" {
		t.Fatalf("разобрался не тот список: %#v", servers)
	}
}

// fakeCache — хранилище списка серверов в памяти, вместо хранилища паролей.
type fakeCache struct {
	mu   sync.Mutex
	list []Server
}

func (c *fakeCache) Save(servers []Server) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list = servers
	return nil
}

func (c *fakeCache) Load() ([]Server, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.list, len(c.list) > 0
}

// Ни одна ссылка не ответила — идём за списком, запомненным в прошлый раз.
func TestResolveFallsBackToRememberedList(t *testing.T) {
	tun := testTunnel(t)
	cache := &fakeCache{}
	tun.SetCache(cache)

	// Настоящие паузы между повторами — секунды, и ждать их в каждой сборке
	// незачем: проверяем не длину пауз, а то, что после них берётся память.
	was := subRetries
	subRetries = []time.Duration{0, 0}
	t.Cleanup(func() { subRetries = was })

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleKey))
	}))
	// Первый раз сервис жив — список должен запомниться.
	if _, _, err := tun.resolve(context.Background(), good.URL); err != nil {
		t.Fatalf("первый заход не удался: %v", err)
	}
	good.Close()
	if len(cache.list) == 0 {
		t.Fatal("список не запомнился")
	}

	// Второй раз сервиса больше нет.
	servers, cached, err := tun.resolve(context.Background(), good.URL)
	if err != nil {
		t.Fatalf("запомненный список не выручил: %v", err)
	}
	if !cached {
		t.Fatal("не отмечено, что список взят из памяти")
	}
	if len(servers) != 1 {
		t.Fatalf("из памяти пришёл не тот список: %#v", servers)
	}
}

// Пометка «через этот сервер Spotify не работает» держится полчаса, а не вечно:
// сервисы переставляют серверы, а Spotify пересматривает свои списки.
func TestBadServerIsForgottenAfterAWhile(t *testing.T) {
	tun := testTunnel(t)

	tun.MarkBad("свежий")
	tun.SetBad(map[string]time.Time{
		"свежий": time.Now(),
		"старый": time.Now().Add(-badFor - time.Minute),
	})

	bad := tun.badList()
	if !bad["свежий"] {
		t.Fatal("свежая пометка потерялась")
	}
	if bad["старый"] {
		t.Fatal("пометка не истекла через полчаса")
	}
	if _, ok := tun.Bad()["старый"]; ok {
		t.Fatal("просроченная пометка уехала бы в настройки")
	}
}
