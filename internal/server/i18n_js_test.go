package server

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestWebDictionariesComplete — у каждого русского текста панели, мастера и
// виджета есть английский вариант в словарях i18n-*.js. Нужен node; без
// него проверка пропускается (на машине разработчика он есть).
func TestWebDictionariesComplete(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "i18ncheck.js"), "web").CombinedOutput()
	if err != nil {
		t.Fatalf("словари неполные:\n%s", out)
	}
	t.Log(string(out))
}
