package links

import "testing"

// Разбор страницы Яндекс.Музыки — самое хрупкое место во всём приложении:
// официального API нет, вёрстку могут поменять когда угодно. Поэтому здесь
// проверяются все встречающиеся варианты разметки, а не один.

func TestParseYandexOgTags(t *testing.T) {
	html := `<html><head>
	<meta property="og:title" content="Кино — Группа крови">
	<meta property="og:description" content="Кино • Трек • 1988">
	</head></html>`

	m := ParseYandexPage(html)
	if m.Artist != "Кино" || m.Title != "Группа крови" {
		t.Fatalf("разобралось как %q — %q", m.Artist, m.Title)
	}
}

// Порядок в заголовке бывает обратным, и понять это можно только по
// описанию, где артист стоит первым.
func TestParseYandexReversedOrder(t *testing.T) {
	html := `<html><head>
	<meta property="og:title" content="Группа крови — Кино. Слушать онлайн на Яндекс Музыке">
	<meta property="og:description" content="Кино • Трек">
	</head></html>`

	m := ParseYandexPage(html)
	if m.Artist != "Кино" || m.Title != "Группа крови" {
		t.Fatalf("разобралось как %q — %q", m.Artist, m.Title)
	}
}

// Атрибуты в теге могут стоять в другом порядке — регулярка обязана это
// пережить, иначе разбор ломается от косметической правки вёрстки.
func TestParseYandexAttributeOrder(t *testing.T) {
	html := `<meta content="Queen — Bohemian Rhapsody" property="og:title">
	         <meta content="Queen • Трек" property="og:description">`

	m := ParseYandexPage(html)
	if m.Artist != "Queen" || m.Title != "Bohemian Rhapsody" {
		t.Fatalf("разобралось как %q — %q", m.Artist, m.Title)
	}
}

// Мета-тегов может не быть вовсе — тогда остаётся заголовок вкладки.
func TestParseYandexFallsBackToTitleTag(t *testing.T) {
	html := `<html><head><title>Слушать Судно — Молчат Дома на Яндекс Музыке</title></head></html>`

	m := ParseYandexPage(html)
	if m.Title == "" {
		t.Fatal("из заголовка вкладки ничего не вышло")
	}
	if q := m.Query(); q == "" {
		t.Fatal("пустой запрос для поиска")
	}
	t.Logf("получилось: %q", m.Query())
}

func TestParseYandexHandlesEntities(t *testing.T) {
	html := `<meta property="og:title" content="Simon &amp; Garfunkel — The Boxer">
	         <meta property="og:description" content="Simon &amp; Garfunkel • Трек">`

	m := ParseYandexPage(html)
	if m.Artist != "Simon & Garfunkel" {
		t.Fatalf("артист разобрался как %q", m.Artist)
	}
}

// Разметка поменялась и разобрать нечего — это не повод падать: заказ уйдёт
// в обычный поиск по тексту.
func TestParseYandexSurvivesGarbage(t *testing.T) {
	for _, html := range []string{"", "<html></html>", "не html вовсе", "<meta property=\"og:title\">"} {
		m := ParseYandexPage(html)
		if m.Title != "" {
			t.Fatalf("из мусора %q что-то разобралось: %+v", html, m)
		}
	}
}

func TestYandexQueryFormat(t *testing.T) {
	cases := map[Meta]string{
		{Artist: "Кино", Title: "Группа крови"}: "Кино - Группа крови",
		{Title: "Группа крови"}:                 "Группа крови",
		{}:                                      "",
	}
	for m, want := range cases {
		if got := m.Query(); got != want {
			t.Errorf("%+v → %q, ждали %q", m, got, want)
		}
	}
}
