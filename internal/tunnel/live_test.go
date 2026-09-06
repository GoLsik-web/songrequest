package tunnel

import (
	"context"
	"os"
	"testing"
	"time"

	"songrequest/internal/errs"
	"songrequest/internal/logx"
)

// Живая проверка: настоящий xray.exe скачивается с GitHub и запускается.
// Ходит в сеть, поэтому включается только по просьбе:
//
//	SONGREQUEST_LIVE=1 go test ./internal/tunnel/ -run TestLive -v
//
// Настоящего ключа здесь нет и быть не может — он секрет стримера. Поэтому
// сервер в проверке выдуманный: до него соединение не дойдёт, но всё
// остальное (скачивание, распаковка, запуск, приём настройки через
// стандартный ввод, поднятый посредник) проверяется по-настоящему.
func TestLiveStartWithFakeServer(t *testing.T) {
	if os.Getenv("SONGREQUEST_LIVE") == "" {
		t.Skip("живая проверка выключена")
	}

	log, err := logx.New(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	tun := New(log, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	tool, err := tun.ensureTool(ctx, "")
	if err != nil {
		t.Fatalf("программа обхода не добылась: %v", err)
	}
	t.Logf("программа обхода: %s", tool)

	fake := Server{
		Kind: "vless", Label: "выдуманный", Host: "198.51.100.1", Port: 443,
		ID:  "11111111-2222-3333-4444-555555555555",
		Net: "tcp", TLS: "reality", SNI: "www.microsoft.com",
		PublicKey: "IdV5nJ1cs6QSHxIL0F0nZ3IdpUxRRUBs6jT4bGqbFBI", ShortID: "01",
		Flow: "xtls-rprx-vision",
	}

	addr, cmd, _, err := tun.launch(ctx, tool, fake)
	if err != nil {
		t.Fatalf("обход не поднялся: %v", err)
	}
	defer kill(cmd)
	t.Logf("посредник поднялся: %s", addr)

	// До выдуманного сервера соединение дойти не может — проверка обязана
	// закончиться внятной ошибкой, а не зависнуть навсегда.
	start := time.Now()
	if err := probe(ctx, addr); err == nil {
		t.Error("проверка связи через выдуманный сервер прошла — так быть не должно")
	} else {
		t.Logf("ожидаемый отказ за %s: %v", time.Since(start).Round(time.Millisecond), err)
	}
}

// Живая проверка настоящей подписки. Ссылка — секрет владельца, поэтому её
// здесь нет: она передаётся через окружение и только на время проверки.
//
//	SONGREQUEST_SUB=https://... go test ./internal/tunnel/ -run TestLiveSubscription -v
//
// Печатает только счёт: сами ключи в вывод попадать не должны.
func TestLiveSubscription(t *testing.T) {
	link := os.Getenv("SONGREQUEST_SUB")
	if link == "" {
		t.Skip("ссылка на подписку не передана")
	}

	log, err := logx.New(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	tun := New(log, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	servers, err := tun.resolve(ctx, link)
	if err != nil {
		t.Fatalf("список серверов не получился: %v", err)
	}
	if len(servers) == 0 {
		t.Fatal("список пустой")
	}

	kinds := map[string]int{}
	for _, s := range servers {
		kinds[s.Kind+"/"+s.Net+"/"+s.TLS]++
	}
	t.Logf("серверов: %d, виды: %v", len(servers), kinds)
}

// Полная живая проверка на настоящей подписке: поднять обход и убедиться, что
// через него отвечает Spotify. Включается двумя переменными окружения сразу —
// ходит и в сеть, и на GitHub за программой обхода:
//
//	SONGREQUEST_LIVE=1 SONGREQUEST_SUB=https://... go test ./internal/tunnel/ -run TestLiveTunnelStart -v
func TestLiveTunnelStart(t *testing.T) {
	link := os.Getenv("SONGREQUEST_SUB")
	if os.Getenv("SONGREQUEST_LIVE") == "" || link == "" {
		t.Skip("живая проверка выключена")
	}

	log, err := logx.New(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	tun := New(log, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	start := time.Now()
	status, err := tun.Start(ctx, link, "", "")
	if err != nil {
		t.Fatalf("обход не поднялся: %v", err)
	}
	defer tun.Stop()

	t.Logf("обход поднялся за %s, посредник %s", time.Since(start).Round(time.Millisecond), status.Addr)
	if !status.On {
		t.Error("обход считает себя выключенным")
	}
}

// Перебор всей подписки: что Spotify отвечает через каждый сервер.
//
// Нужен, чтобы понимать, сколько серверов вообще годятся. Живьём выяснилось,
// что через часть серверов Spotify отвечает 403 «Spotify is unavailable in
// this country» — обход при этом работает, а приложение бесполезно.
//
//	SONGREQUEST_LIVE=1 SONGREQUEST_SUB=https://... go test ./internal/tunnel/ -run TestLiveEachServer -v
func TestLiveEachServer(t *testing.T) {
	link := os.Getenv("SONGREQUEST_SUB")
	if os.Getenv("SONGREQUEST_LIVE") == "" || link == "" {
		t.Skip("живая проверка выключена")
	}

	log, err := logx.New(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	tun := New(log, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	servers, err := tun.resolve(ctx, link)
	if err != nil {
		t.Fatal(err)
	}
	tool, err := tun.ensureTool(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	good := 0
	for _, s := range servers {
		if err := reachable(ctx, s); err != nil {
			t.Logf("%-45s порт молчит", s.Label)
			continue
		}
		addr, cmd, _, err := tun.launch(ctx, tool, s)
		if err != nil {
			t.Logf("%-45s обход не поднялся", s.Label)
			continue
		}
		if err := probe(ctx, addr); err != nil {
			_, text := errs.Describe(err)
			t.Logf("%-45s %s", s.Label, text)
		} else {
			good++
			t.Logf("%-45s ГОДИТСЯ", s.Label)
		}
		killAndReap(cmd)
	}
	t.Logf("годных серверов: %d из %d", good, len(servers))
}
