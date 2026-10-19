package errs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"songrequest/internal/i18n"
)

// В английском режиме панель показывает английский текст, код тот же.
func TestDescribeEnglish(t *testing.T) {
	i18n.SetLang(i18n.EN)
	defer i18n.SetLang(i18n.RU)

	inner := Newf(SpotifyRateLimit, "Spotify попросил короткую паузу (осталось %s). Приложение подождёт само.", "3s")
	outer := Wrap(SpotifySnapshot, "Не смог запомнить, что играло в Spotify.", inner)
	code, text := Describe(outer)
	if code != SpotifySnapshot {
		t.Fatalf("код %s, ждали %s", code, SpotifySnapshot)
	}
	want := "Couldn't remember what was playing in Spotify. Spotify asked for a short pause (3s left). The app will wait on its own."
	if text != want {
		t.Fatalf("текст %q, ждали %q", text, want)
	}
	if !strings.HasPrefix(outer.Error(), "SP-09: Не смог") {
		t.Errorf("журнал должен остаться русским: %q", outer.Error())
	}
	if got := New(SpotifyQuota, "x").UserText(); got != "SP-20 · x" {
		t.Errorf("UserText: %q", got)
	}
	if _, text := Describe(HTTPStatus(UpdateCheck, "GitHub", 503)); text != "GitHub is having trouble right now. Try again a bit later." {
		t.Errorf("HTTPStatus: %q", text)
	}
}

// Русский режим — байт-в-байт прежний текст.
func TestDescribeRussianUnchanged(t *testing.T) {
	i18n.SetLang(i18n.RU)
	e := Newf(DonationsUnreachable, "DonatePay ответил ошибкой (%d).", 500)
	if _, text := Describe(e); text != "DonatePay ответил ошибкой (500)." {
		t.Fatalf("%q", text)
	}
	if got := HTTPStatusText("Twitch", 429); got != "Twitch временно не пускает. Попробуй чуть позже." {
		t.Fatalf("%q", got)
	}
}

// Каждый текст ошибки с кодом, написанный буквально, должен иметь перевод.
func TestAllMessagesTranslated(t *testing.T) {
	argIdx := map[string]int{"New": 1, "Wrap": 1, "Newf": 1, "Wrapf": 2, "Phrase": 0}
	root := filepath.Join("..")
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		fs := token.NewFileSet()
		f, perr := parser.ParseFile(fs, p, nil, 0)
		if perr != nil {
			return nil // чужая правка в процессе — не наше дело
		}
		ast.Inspect(f, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			s, ok := c.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := s.X.(*ast.Ident)
			i, known := argIdx[s.Sel.Name]
			if !ok || x.Name != "errs" || !known || len(c.Args) <= i {
				return true
			}
			if v, ok := fold(c.Args[i]); ok && !i18n.Has(v) {
				t.Errorf("%s: нет английского текста для %q", fs.Position(c.Pos()), v)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func fold(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(x.Value)
		return v, err == nil
	case *ast.BinaryExpr:
		a, ok1 := fold(x.X)
		b, ok2 := fold(x.Y)
		return a + b, ok1 && ok2 && x.Op == token.ADD
	case *ast.ParenExpr:
		return fold(x.X)
	}
	return "", false
}
