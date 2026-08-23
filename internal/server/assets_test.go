package server

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// Панель вшита в бинарник. Если файл добавили в папку, но забыли сослаться —
// или сослались на несуществующий, — на машине разработчика всё работает
// (файл лежит рядом), а у стримера панель тихо ломается. Проверяем ссылки.

func TestEmbeddedAssetsExist(t *testing.T) {
	web, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}

	page, err := fs.ReadFile(web, "index.html")
	if err != nil {
		t.Fatal(err)
	}

	// Ссылки вида /static/... в разметке должны существовать среди вшитых.
	refs := regexp.MustCompile(`/static/([\w./-]+)`).FindAllStringSubmatch(string(page), -1)
	if len(refs) == 0 {
		t.Fatal("в разметке не нашлось ни одной ссылки на статику — так не бывает")
	}

	for _, m := range refs {
		name := m[1]
		if _, err := fs.Stat(web, name); err != nil {
			t.Errorf("страница ссылается на %s, но такого файла в бинарнике нет", m[0])
		}
	}
}

func TestFontsAndIconsAreEmbedded(t *testing.T) {
	web, _ := fs.Sub(webFS, "web")

	// Шрифты и иконки лежат локально не для красоты: панель обязана
	// открываться без интернета.
	must := []string{"fonts/fonts.css", "icons/sprite.svg", "favicon.svg"}
	for _, name := range must {
		if _, err := fs.Stat(web, name); err != nil {
			t.Errorf("не вшит обязательный файл %s", name)
		}
	}

	css, err := fs.ReadFile(web, "fonts/fonts.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(css), "http://") || strings.Contains(string(css), "https://") {
		t.Fatal("шрифты тянутся из сети — панель перестанет работать без интернета")
	}

	// Каждый файл шрифта, упомянутый в css, должен быть на месте.
	for _, m := range regexp.MustCompile(`url\('([^']+)'\)`).FindAllStringSubmatch(string(css), -1) {
		if _, err := fs.Stat(web, "fonts/"+m[1]); err != nil {
			t.Errorf("в fonts.css указан %s, а файла нет", m[1])
		}
	}
}

func TestIconSpriteHasEveryIconThePanelUses(t *testing.T) {
	web, _ := fs.Sub(webFS, "web")

	sprite, err := fs.ReadFile(web, "icons/sprite.svg")
	if err != nil {
		t.Fatal(err)
	}
	script, err := fs.ReadFile(web, "app.js")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := fs.ReadFile(web, "index.html")

	// Иконка, которой нет в спрайте, не покажется никак — ни ошибки, ни следа.
	used := regexp.MustCompile(`#i-([\w-]+)`).FindAllStringSubmatch(string(script)+string(page), -1)
	for _, m := range used {
		if !strings.Contains(string(sprite), `id="i-`+m[1]+`"`) {
			t.Errorf("панель просит иконку %q, а в спрайте её нет", m[1])
		}
	}
}
