package tunnel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Проверка сервера обхода — это не «дошли ли до Spotify», а «согласен ли
// Spotify с нами разговаривать с этого адреса».
//
// Живьём 30.08: обход поднялся, в панели «Обход работает», а приложение не
// могло даже запомнить играющий трек. Сервер стоял в дата-центре, который
// Spotify считает неподходящей страной, и на всё отвечал 403 «Spotify is
// unavailable in this country». Проверка принимала любой ответ и оставляла
// такой сервер выбранным.

// withFakeSpotify подменяет проверочный адрес поддельным Spotify и отдаёт
// адрес «посредника»: в проверке им работает обычный http-прокси, которым
// притворяется тот же сервер.
func withFakeSpotify(t *testing.T, status int, body string) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	was := probeURL
	probeURL = "http://spotify.проверка/v1/me"
	t.Cleanup(func() { probeURL = was })

	return "http://" + strings.TrimPrefix(srv.URL, "http://")
}

// 401 — тот самый ответ, которого мы ждём: Spotify нас видит и просит вход.
func TestProbeAcceptsUnauthorized(t *testing.T) {
	addr := withFakeSpotify(t, http.StatusUnauthorized,
		`{"error":{"status":401,"message":"No token provided"}}`)

	if err := probe(context.Background(), addr); err != nil {
		t.Fatalf("нормальный ответ Spotify забракован: %v", err)
	}
}

// 403 «недоступен в этой стране» — сервер обхода не годится, даже если
// соединение через него прекрасное.
func TestProbeRejectsCountryBlock(t *testing.T) {
	addr := withFakeSpotify(t, http.StatusForbidden,
		`{ "error" : { "status" : 403, "message" : "Spotify is unavailable in this country" } }`)

	err := probe(context.Background(), addr)
	if err == nil {
		t.Fatal("сервер, через который Spotify отказывает, признан рабочим")
	}
	if !strings.Contains(err.Error(), "страну сервера") {
		t.Errorf("человеку не объяснили причину: %v", err)
	}
}

// Поломка на стороне Spotify — тоже повод взять другой сервер.
func TestProbeRejectsServerError(t *testing.T) {
	addr := withFakeSpotify(t, http.StatusBadGateway, "")

	if err := probe(context.Background(), addr); err == nil {
		t.Fatal("ответ 502 признан рабочим")
	}
}

// Негодные серверы приложение запоминает и больше не предлагает.
func TestSortPreferredSkipsBad(t *testing.T) {
	servers := []Server{
		{Kind: "vless", Label: "первый", Host: "198.51.100.1", Port: 443},
		{Kind: "vless", Label: "второй", Host: "198.51.100.2", Port: 443},
		{Kind: "vless", Label: "третий", Host: "198.51.100.3", Port: 443},
	}
	bad := map[string]bool{servers[0].String(): true}

	order := sortPreferred(servers, servers[2].String(), bad)
	if len(order) != 2 {
		t.Fatalf("серверов в очереди %d, а ждали 2", len(order))
	}
	if order[0].Label != "третий" {
		t.Errorf("первым должен идти прошлый рабочий, а идёт %q", order[0].Label)
	}
	for _, s := range order {
		if s.Label == "первый" {
			t.Error("негодный сервер снова в очереди")
		}
	}

	// Если негодны все — пробуем заново весь список: лучше так, чем сказать
	// «серверов нет» при полной подписке.
	all := map[string]bool{}
	for _, s := range servers {
		all[s.String()] = true
	}
	if got := sortPreferred(servers, "", all); len(got) != len(servers) {
		t.Fatalf("когда негодны все, ждали полный список, а вышло %d", len(got))
	}
}
