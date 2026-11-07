package desktop

import (
	"bytes"
	"os"
	"testing"
)

func TestIconUpdateReplacesDifferentContentsOfSameSize(t *testing.T) {
	dir := t.TempDir()
	path, err := IconFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, make([]byte, len(iconICO)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = IconFile(dir); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, iconICO) {
		t.Fatal("old icon retained after update", err)
	}
}
