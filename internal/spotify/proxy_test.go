package spotify

import (
	"net/url"
	"strings"
	"testing"
)

// Адрес прокси вставляют как придётся. Отвечать на это словом «ошибка»
// нельзя: человек не поймёт, что именно он написал не так.
func TestProxyAddressIsForgivingAndExplains(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		scheme string
		host   string
		bad    string // что должно быть упомянуто в отказе
	}{
		{name: "просто адрес и порт", raw: "1.2.3.4:1080", scheme: "socks5", host: "1.2.3.4:1080"},
		{name: "полный socks5", raw: "socks5://1.2.3.4:1080", scheme: "socks5", host: "1.2.3.4:1080"},
		{name: "с логином и паролем", raw: "socks5://user:pass@host.tld:1080", scheme: "socks5", host: "host.tld:1080"},
		{name: "http-прокси", raw: "http://host.tld:3128", scheme: "http", host: "host.tld:3128"},
		{name: "лишние пробелы", raw: "  1.2.3.4:1080  ", scheme: "socks5", host: "1.2.3.4:1080"},

		{name: "без порта", raw: "socks5://host.tld", bad: "порт"},
		{name: "неизвестный вид", raw: "wireguard://host.tld:51820", bad: "wireguard"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, err := ParseProxy(c.raw)

			if c.bad != "" {
				if err == nil {
					t.Fatalf("кривой адрес принят: %q", c.raw)
				}
				if !strings.Contains(err.Error(), c.bad) {
					t.Fatalf("отказ не объясняет причину (%q): %v", c.bad, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("нормальный адрес не принят: %v", err)
			}
			if u.Scheme != c.scheme || u.Host != c.host {
				t.Fatalf("разобрано неверно: %s // %s", u.Scheme, u.Host)
			}
		})
	}
}

// Пароль от прокси — такой же секрет, как ключ доступа. Ни в панель, ни в
// лог, ни в выгрузку он попадать не должен.
func TestProxyPasswordIsNeverShown(t *testing.T) {
	u, err := ParseProxy("socks5://streamer:очень-секретно@host.tld:1080")
	if err != nil {
		t.Fatal(err)
	}

	shown := Hide(u)
	if strings.Contains(shown, "очень-секретно") {
		t.Fatalf("пароль виден: %s", shown)
	}
	// Всё остальное должно остаться узнаваемым, иначе непонятно, тот ли
	// прокси включён.
	for _, want := range []string{"streamer", "host.tld", "1080", "socks5"} {
		if !strings.Contains(shown, want) {
			t.Fatalf("из адреса пропало %q: %s", want, shown)
		}
	}
}

// Пустая строка — это «ходить напрямую», а не ошибка.
func TestEmptyProxyMeansDirect(t *testing.T) {
	if _, err := url.Parse(""); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseProxy(""); err == nil {
		t.Fatal("пустой адрес должен отвергаться разбором — прямое соединение включается выше")
	}
}
