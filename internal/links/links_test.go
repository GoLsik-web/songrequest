package links

import "testing"

func TestFindSpotify(t *testing.T) {
	cases := []string{
		"https://open.spotify.com/track/11dFghVXANMlKmJXsNCbNl",
		"https://open.spotify.com/intl-ru/track/11dFghVXANMlKmJXsNCbNl?si=abc123",
		"spotify:track:11dFghVXANMlKmJXsNCbNl",
		"врубай https://open.spotify.com/track/11dFghVXANMlKmJXsNCbNl пж",
	}
	for _, text := range cases {
		l, ok := Find(text)
		if !ok || l.Kind != Spotify {
			t.Fatalf("%q не распознано как Spotify: %+v", text, l)
		}
		if l.ID != "11dFghVXANMlKmJXsNCbNl" {
			t.Fatalf("%q → id %q", text, l.ID)
		}
	}
}

func TestFindYouTube(t *testing.T) {
	cases := map[string]string{
		"https://www.youtube.com/watch?v=fJ9rUzIMcZQ":           "fJ9rUzIMcZQ",
		"https://youtu.be/fJ9rUzIMcZQ":                          "fJ9rUzIMcZQ",
		"https://youtu.be/fJ9rUzIMcZQ?si=xyz":                   "fJ9rUzIMcZQ",
		"https://music.youtube.com/watch?v=fJ9rUzIMcZQ&list=RD": "fJ9rUzIMcZQ",
		"https://www.youtube.com/shorts/fJ9rUzIMcZQ":            "fJ9rUzIMcZQ",
		"https://www.youtube.com/watch?t=42&v=fJ9rUzIMcZQ":      "fJ9rUzIMcZQ",
		"вот это https://youtu.be/fJ9rUzIMcZQ, врубай":          "fJ9rUzIMcZQ",
	}
	for text, want := range cases {
		l, ok := Find(text)
		if !ok || l.Kind != YouTube {
			t.Fatalf("%q не распознано как YouTube: %+v", text, l)
		}
		if l.ID != want {
			t.Fatalf("%q → id %q, ждали %q", text, l.ID, want)
		}
	}
}

func TestFindYandex(t *testing.T) {
	cases := []string{
		"https://music.yandex.ru/album/12345/track/67890",
		"https://music.yandex.com/album/12345/track/67890?from=serp",
		"https://music.yandex.ru/track/67890",
	}
	for _, text := range cases {
		l, ok := Find(text)
		if !ok || l.Kind != Yandex {
			t.Fatalf("%q не распознано как Яндекс.Музыка: %+v", text, l)
		}
		if l.ID != "67890" {
			t.Fatalf("%q → id %q", text, l.ID)
		}
	}
}

func TestFindVK(t *testing.T) {
	for _, text := range []string{
		"https://vk.com/video-123_456",
		"https://vk.ru/audio123_456",
		"https://vkvideo.ru/video-1_2",
	} {
		if l, ok := Find(text); !ok || l.Kind != VK {
			t.Fatalf("%q не распознано как VK: %+v", text, l)
		}
	}
}

func TestPlainTextIsNotALink(t *testing.T) {
	for _, text := range []string{
		"Queen - Bohemian Rhapsody",
		"Кино — Группа крови",
		"",
		"смотри на сайте example.com",
	} {
		if l, ok := Find(text); ok {
			t.Fatalf("%q принято за ссылку: %+v", text, l)
		}
	}
}

// Зрители копируют ссылку вместе со знаком препинания — адрес от этого
// ломается, а трек не находится.
func TestTrailingPunctuationIsTrimmed(t *testing.T) {
	l, ok := Find("врубай (https://music.yandex.ru/album/1/track/2).")
	if !ok {
		t.Fatal("ссылка не нашлась")
	}
	if want := "https://music.yandex.ru/album/1/track/2"; l.URL != want {
		t.Fatalf("адрес %q, ждали %q", l.URL, want)
	}
}

// Рекламные метки не часть ссылки на трек.
func TestTrackingParamsAreDropped(t *testing.T) {
	l, _ := Find("https://music.yandex.ru/album/1/track/2?utm_source=share&from=serp&reqid=xxx")
	if want := "https://music.yandex.ru/album/1/track/2"; l.URL != want {
		t.Fatalf("адрес %q, ждали %q", l.URL, want)
	}
}

// Из текста ссылку надо вырезать: по «врубай вот это пж» искать нечего,
// а «врубай https://... пж» ломает поиск по словам.
func TestStripRemovesLinks(t *testing.T) {
	cases := map[string]string{
		"врубай https://youtu.be/abc123abcde пж":     "врубай пж",
		"spotify:track:11dFghVXANMlKmJXsNCbNl":       "",
		"Queen - Bohemian Rhapsody":                  "Queen - Bohemian Rhapsody",
		"https://music.yandex.ru/track/1 Кино Судно": "Кино Судно",
	}
	for in, want := range cases {
		if got := Strip(in); got != want {
			t.Errorf("%q → %q, ждали %q", in, got, want)
		}
	}
}

// Зритель за баллы может написать что угодно, и адрес из его заказа
// приложение открывает само — со своего компьютера и с куками стримера.
// Раньше домен искался подстрокой в любом месте адреса, включая путь.
func TestForeignHostIsNotOurs(t *testing.T) {
	bad := []string{
		"https://192.168.1.1/music.yandex.ru/track/1",
		"https://злой.example/vk.com/audio",
		"http://127.0.0.1:8977/youtube.com/watch?v=dQw4w9WgXcQ",
		"https://notvk.com/audio1_2",
		"https://злойyoutube.com/watch?v=dQw4w9WgXcQ",
	}
	for _, raw := range bad {
		if link, ok := Find(raw); ok {
			t.Errorf("%q принято за свою ссылку: %s %s", raw, link.Kind, link.URL)
		}
	}
}

// А настоящие ссылки, в том числе с поддомена, разбираться обязаны.
func TestRealHostsStillParse(t *testing.T) {
	good := map[string]Kind{
		"https://music.yandex.ru/album/1/track/2":       Yandex,
		"https://vk.com/audio1_2":                       VK,
		"https://m.youtube.com/watch?v=dQw4w9WgXcQ":     YouTube,
		"https://music.youtube.com/watch?v=dQw4w9WgXcQ": YouTube,
	}
	for raw, kind := range good {
		link, ok := Find(raw)
		if !ok || link.Kind != kind {
			t.Errorf("%q не разобралось как %s (получили %q, %v)", raw, kind, link.Kind, ok)
		}
	}
}
