package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestEveryKeyHasEnglish — каждый текст, обёрнутый в i18n.T / i18n.F /
// i18n.English в коде, обязан иметь английский вариант. Иначе в
// английском режиме вылезет русский.
func TestEveryKeyHasEnglish(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	missing := map[string]string{}
	count := 0
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			n := d.Name()
			if n == "dist" || n == "tools" || strings.HasPrefix(n, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			name := ""
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				if id, ok := fn.X.(*ast.Ident); ok && id.Name == "i18n" {
					name = fn.Sel.Name
				}
			case *ast.Ident:
				if strings.HasSuffix(p, filepath.Join("i18n", "i18n.go")) {
					return true
				}
			}
			if name != "T" && name != "F" && name != "English" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			count++
			if !Has(s) {
				missing[s] = fset.Position(lit.Pos()).String()
			}
			return true
		})
		return nil
	})
	for k, pos := range missing {
		t.Errorf("нет английского текста для %q (%s)", k, pos)
	}
	t.Logf("проверено вызовов: %d", count)
}

// TestEnglishHasNoCyrillic — в английских текстах не осталось русских букв
// и плейсхолдеры формата совпадают.
func TestEnglishHasNoCyrillic(t *testing.T) {
	for k, v := range en {
		for _, r := range v {
			if (r >= 'А' && r <= 'я') || r == 'ё' || r == 'Ё' {
				t.Errorf("кириллица в переводе %q → %q", k, v)
				break
			}
		}
		if strings.Count(k, "%") != strings.Count(v, "%") {
			t.Errorf("разное число %% в %q → %q", k, v)
		}
	}
}

func TestSwitch(t *testing.T) {
	defer SetLang(RU)
	register(map[string]string{"тест %d": "test %d"})
	if F("тест %d", 1) != "тест 1" {
		t.Fatal("ru должен остаться как есть")
	}
	SetLang("en")
	if F("тест %d", 1) != "test 1" || T("нет такого") != "нет такого" {
		t.Fatal("en не переключился")
	}
	if Plural(1, [3]string{"трек", "трека", "треков"}, [2]string{"track", "tracks"}) != "track" ||
		Plural(5, [3]string{"трек", "трека", "треков"}, [2]string{"track", "tracks"}) != "tracks" {
		t.Fatal("plural en")
	}
	SetLang("xx")
	if Lang() != RU {
		t.Fatal("неизвестный язык должен давать ru")
	}
	delete(en, "тест %d")
}
