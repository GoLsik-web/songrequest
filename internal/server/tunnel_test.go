package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"songrequest/internal/config"
	"songrequest/internal/errs"
	"songrequest/internal/tunnel"
)

// Ключ от VPN — пароль стримера. Проверяем не «работает ли обход» (для этого
// нужен настоящий ключ и настоящий сервер), а то, что ключ не расползается по
// диску и что панель отказывает понятно.

// withTunnel добавляет к тестовому серверу обход и поддельное хранилище
// паролей: настоящее лезло бы в «Диспетчер учётных данных» машины.
func withTunnel(t *testing.T) (*Server, *fakeSecrets) {
	t.Helper()
	srv, _ := newTestServer(t, nil, func(w http.ResponseWriter, r *http.Request) {})
	keys := &fakeSecrets{}
	srv.secrets = keys
	srv.tunnel = tunnel.New(srv.log, t.TempDir())
	return srv, keys
}

// call дёргает ручку панели и возвращает ответ.
func call(t *testing.T, h http.HandlerFunc, body string) (int, map[string]any) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(http.MethodPost, "/", nil)
	} else {
		r = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	}
	w := httptest.NewRecorder()
	h(w, r)

	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// Пустое поле — не повод лезть в сеть: человек должен сразу узнать, что
// вставить забыли.
func TestTunnelOnWithoutKeyAsksForOne(t *testing.T) {
	srv, _ := withTunnel(t)

	code, out := call(t, srv.handleTunnelOn, `{}`)
	if code != http.StatusBadRequest {
		t.Fatalf("ответ %d, а ждали отказ", code)
	}
	if out["code"] != "OB-01" {
		t.Fatalf("код %v, а ждали OB-01", out["code"])
	}
	if srv.cfg.Get().TunnelOn {
		t.Error("обход остался включённым, хотя включать было нечем")
	}
}

// Испорченный ключ отсеиваем до сохранения: иначе человек ждал бы минуту
// перебора серверов ради «ничего не вышло».
func TestTunnelOnRejectsBrokenKey(t *testing.T) {
	srv, keys := withTunnel(t)

	code, out := call(t, srv.handleTunnelOn, `{"key":"мой ключ от впн"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("ответ %d, а ждали отказ", code)
	}
	if out["code"] != "OB-02" {
		t.Fatalf("код %v, а ждали OB-02", out["code"])
	}
	if len(keys.data) != 0 {
		t.Error("непонятный ключ всё-таки сохранили")
	}
	if srv.cfg.Get().TunnelKeyHint != "" {
		t.Error("в настройках появилась подпись к ключу, которого нет")
	}
}

// Главное: сам ключ ложится только в хранилище паролей. Ни в config.json, ни
// в состоянии панели его быть не должно — файл настроек уезжает в архив
// диагностики, а панель бывает видно на стриме.
func TestTunnelKeyStaysOutOfConfigAndPanel(t *testing.T) {
	srv, keys := withTunnel(t)

	const key = "vless://11111111-2222-3333-4444-555555555555@198.51.100.7:443" +
		"?security=reality&sni=www.microsoft.com&pbk=IdV5nJ1cs6QSHxIL0F0nZ3IdpUxRRUBs6jT4bGqbFBI&sid=01#Мой"

	if err := srv.saveTunnelKey(key); err != nil {
		t.Fatalf("ключ не сохранился: %v", err)
	}

	var saved savedKey
	if err := keys.GetJSON(keyringTunnel, &saved); err != nil || saved.Key != key {
		t.Fatalf("в хранилище паролей ключа нет: %v", err)
	}

	hint := srv.cfg.Get().TunnelKeyHint
	if hint == "" {
		t.Error("подписи к ключу нет — панели нечего показать")
	}
	if strings.Contains(hint, "11111111") {
		t.Errorf("в подписи виден опознавательный номер: %q", hint)
	}

	raw, err := os.ReadFile(filepath.Join(srv.dataDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "11111111") {
		t.Fatal("ключ утёк в файл настроек")
	}

	srv.syncTunnel("")
	snap := srv.state.Snapshot()
	if !snap.Tunnel.HasKey {
		t.Error("панель не знает, что ключ вставлен")
	}
	if strings.Contains(snap.Tunnel.Hint, "11111111") {
		t.Error("ключ виден в панели")
	}
}

// Ссылку на подписку тоже принимаем, и в подписи остаётся только адрес
// сервиса — без части, по которой список серверов и выдаётся.
func TestTunnelHintForSubscriptionHidesPath(t *testing.T) {
	hint, err := tunnelHint("https://sub.example.com/link/AbCdEfGh?flow=xtls")
	if err != nil {
		t.Fatalf("ссылка на подписку не принялась: %v", err)
	}
	if hint != "подписка sub.example.com" {
		t.Fatalf("подпись %q", hint)
	}
}

// «Забыть ключ» должно стирать всё: и сам ключ, и подпись, и запомненный
// сервер. Иначе панель будет уверять, что ключ есть.
func TestTunnelForgetClearsEverything(t *testing.T) {
	srv, keys := withTunnel(t)

	const key = "trojan://пароль@198.51.100.9:443?security=tls&sni=example.com#Сервер"
	if err := srv.saveTunnelKey(key); err != nil {
		t.Fatalf("ключ не сохранился: %v", err)
	}

	code, _ := call(t, srv.handleTunnelForget, "")
	if code != http.StatusOK {
		t.Fatalf("ответ %d", code)
	}
	if len(keys.data) != 0 {
		t.Error("ключ остался в хранилище паролей")
	}
	cfg := srv.cfg.Get()
	if cfg.TunnelOn || cfg.TunnelKeyHint != "" || cfg.TunnelServer != "" {
		t.Errorf("в настройках остались следы: %+v", cfg)
	}
}

// Смена сервера обхода — только на «Spotify недоступен в этой стране» и
// только при работающем обходе. Иначе приложение начало бы дёргать серверы на
// каждый обрыв сети.
func TestReselectOnlyOnCountryRefusal(t *testing.T) {
	srv, _ := withTunnel(t)

	// Обход выключен: даже нужный отказ ничего не запускает.
	srv.noteSpotifyError(errs.New(errs.SpotifyCountry, "Spotify не работает из этой страны."))
	if srv.reselects.Load() != 0 {
		t.Fatal("перебор серверов пошёл при выключенном обходе")
	}

	// Чужая ошибка не повод менять сервер.
	srv.noteSpotifyError(errs.New(errs.SpotifyUnreachable, "Spotify не отвечает."))
	if srv.reselects.Load() != 0 {
		t.Fatal("сервер меняется от обычного обрыва связи")
	}
	if srv.reselecting.Load() {
		t.Fatal("приложение считает, что перебор идёт, хотя он не начинался")
	}
}

// Отказ «Spotify не работает из этой страны» при выключенном обходе обязан
// поднимать обход, а не проходить мимо.
//
// Живьём 31.08 это и подвело: приложение решило, что обход не нужен (Spotify
// отвечал напрямую через VPN стримера), а список плейлистов в ту же секунду
// получил SP-16 — и никто на это не отреагировал, потому что разбор дороги
// висел только на опросе плеера.
func TestCountryRefusalWakesTunnel(t *testing.T) {
	srv, keys := withTunnel(t)

	// Ключ есть и обход разрешён — приложению есть что поднимать.
	if err := keys.PutJSON(keyringTunnel, savedKey{Key: "vless://id@example.com:443"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.cfg.Update(func(c *config.Config) { c.TunnelOn = true }); err != nil {
		t.Fatal(err)
	}

	srv.noteSpotifyError(errs.New(errs.SpotifyCountry, "Spotify не работает из этой страны."))

	if !srv.reviving.Load() {
		t.Fatal("обход не начали поднимать на отказ по стране")
	}

	// Подъём идёт в стороне и с паузами; чтобы он не жил после проверки,
	// выключаем обход в настройках — на следующем круге он это увидит и уйдёт.
	if err := srv.cfg.Update(func(c *config.Config) { c.TunnelOn = false }); err != nil {
		t.Fatal(err)
	}
}

// А при выключенном обходе в настройках — не поднимать: человек мог выключить
// его нарочно, и лезть в сеть против его решения нельзя.
func TestCountryRefusalRespectsSwitch(t *testing.T) {
	srv, keys := withTunnel(t)
	if err := keys.PutJSON(keyringTunnel, savedKey{Key: "vless://id@example.com:443"}); err != nil {
		t.Fatal(err)
	}

	srv.noteSpotifyError(errs.New(errs.SpotifyCountry, "Spotify не работает из этой страны."))

	if srv.reviving.Load() {
		t.Fatal("обход полез подниматься, хотя выключен в настройках")
	}
}
