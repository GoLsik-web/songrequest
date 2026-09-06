package tunnel

import (
	"encoding/base64"
	"strings"
	"testing"
)

// Ключи здесь выдуманные: адреса из документации, номера случайные. Настоящий
// ключ стримера — секрет и в репозитории ему делать нечего.

func TestParseVLESSReality(t *testing.T) {
	key := "vless://11111111-2222-3333-4444-555555555555@198.51.100.7:443" +
		"?type=tcp&security=reality&sni=www.microsoft.com&fp=chrome" +
		"&pbk=abcdef&sid=0123&flow=xtls-rprx-vision#%D0%9D%D0%B8%D0%B4%D0%B5%D1%80%D0%BB%D0%B0%D0%BD%D0%B4%D1%8B"

	list, err := ParseKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("разобрал %d серверов, ждали один", len(list))
	}
	s := list[0]
	if s.Kind != "vless" || s.Host != "198.51.100.7" || s.Port != 443 {
		t.Errorf("не тот сервер: %+v", s)
	}
	if s.ID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("не тот uuid: %s", s.ID)
	}
	if s.TLS != "reality" || s.PublicKey != "abcdef" || s.ShortID != "0123" {
		t.Errorf("reality разобран неверно: %+v", s)
	}
	if s.Flow != "xtls-rprx-vision" || s.SNI != "www.microsoft.com" || s.Fingerprint != "chrome" {
		t.Errorf("параметры соединения разобраны неверно: %+v", s)
	}
	// Подпись сервера человек видит в панели, поэтому она обязана
	// раскодироваться из процентов обратно в буквы.
	if s.Label != "Нидерланды" {
		t.Errorf("подпись сервера: %q", s.Label)
	}
}

func TestParseVLESSWebSocket(t *testing.T) {
	key := "vless://11111111-2222-3333-4444-555555555555@example.com:8443" +
		"?type=ws&security=tls&path=%2Fray&host=cdn.example.com#WS"

	list, err := ParseKey(key)
	if err != nil {
		t.Fatal(err)
	}
	s := list[0]
	if s.Net != "ws" || s.Path != "/ray" || s.HostHdr != "cdn.example.com" {
		t.Errorf("websocket разобран неверно: %+v", s)
	}
	// Имя сервера в ключе не указано — берём подставной адрес из заголовка,
	// иначе рукопожатие уйдёт на голый адрес и сорвётся.
	if s.SNI != "cdn.example.com" {
		t.Errorf("имя сервера: %q", s.SNI)
	}
}

func TestParseShadowsocksBothForms(t *testing.T) {
	creds := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:тайна"))
	modern := "ss://" + creds + "@198.51.100.9:8388#Сервер"
	old := "ss://" + base64.StdEncoding.EncodeToString(
		[]byte("aes-256-gcm:тайна@198.51.100.9:8388")) + "#Сервер"

	for name, key := range map[string]string{"нынешняя": modern, "старая": old} {
		list, err := ParseKey(key)
		if err != nil {
			t.Fatalf("%s запись: %v", name, err)
		}
		s := list[0]
		if s.Kind != "shadowsocks" || s.Host != "198.51.100.9" || s.Port != 8388 {
			t.Errorf("%s запись, не тот сервер: %+v", name, s)
		}
		if s.Method != "aes-256-gcm" || s.ID != "тайна" {
			t.Errorf("%s запись, не тот пароль: %+v", name, s)
		}
	}
}

func TestParseTrojanAlwaysTLS(t *testing.T) {
	list, err := ParseKey("trojan://пароль@example.com:443#T")
	if err != nil {
		t.Fatal(err)
	}
	if list[0].TLS != "tls" {
		t.Errorf("у trojan шифрование включено всегда, получили %q", list[0].TLS)
	}
}

func TestParseVMess(t *testing.T) {
	body := base64.StdEncoding.EncodeToString([]byte(
		`{"v":"2","ps":"Тест","add":"example.com","port":"443","id":"11111111-2222-3333-4444-555555555555",` +
			`"aid":"0","net":"ws","path":"/x","host":"cdn.example.com","tls":"tls"}`))

	list, err := ParseKey("vmess://" + body)
	if err != nil {
		t.Fatal(err)
	}
	s := list[0]
	if s.Kind != "vmess" || s.Port != 443 || s.Net != "ws" || s.TLS != "tls" {
		t.Errorf("vmess разобран неверно: %+v", s)
	}
	// Порт в этих ключах записывают то числом, то строкой — обе записи обязаны
	// читаться, иначе половина ключей молча не работает.
	if s.Cipher != "auto" {
		t.Errorf("способ шифрования по умолчанию: %q", s.Cipher)
	}
}

func TestParseSubscriptionBody(t *testing.T) {
	// Подписку отдают одной строкой в base64 — так делают панели VPN-сервисов.
	list := "vless://11111111-2222-3333-4444-555555555555@a.example.com:443?security=tls#A\n" +
		"trojan://пароль@b.example.com:443#B\n" +
		"мусор, который разобрать нельзя\n"
	packed := base64.StdEncoding.EncodeToString([]byte(list))

	servers, err := ParseKey(packed)
	if err != nil {
		t.Fatal(err)
	}
	// Строку-мусор пропускаем молча: в подписках попадаются заголовки и
	// незнакомые виды ключей, и отказываться из-за них от рабочих серверов
	// нельзя.
	if len(servers) != 2 {
		t.Fatalf("разобрал %d серверов, ждали два: %+v", len(servers), servers)
	}
	if servers[0].Label != "A" || servers[1].Label != "B" {
		t.Errorf("не те серверы: %+v", servers)
	}
}

func TestParseKeyErrors(t *testing.T) {
	if _, err := ParseKey("   "); err == nil {
		t.Error("пустой ключ обязан быть ошибкой")
	}
	if _, err := ParseKey("просто текст"); err == nil {
		t.Error("текст без ключа обязан быть ошибкой")
	}
	// Сообщение читает не программист, поэтому в нём должно быть сказано, что
	// именно вставлять.
	_, err := ParseKey("http://example.com/ключ")
	if err == nil || !strings.Contains(err.Error(), "vless://") {
		t.Errorf("непонятная подсказка: %v", err)
	}
}

func TestLooksLikeSubscription(t *testing.T) {
	if !LooksLikeSubscription("https://sub.example.com/abc") {
		t.Error("адрес подписки не узнан")
	}
	if LooksLikeSubscription("vless://x@a.b:443") {
		t.Error("обычный ключ принят за подписку")
	}
}
