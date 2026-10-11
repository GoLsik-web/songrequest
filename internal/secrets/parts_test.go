package secrets

import (
	"strings"
	"testing"
)

// Большая запись (список серверов обхода) кладётся по частям и читается целой.
func TestBigValueRoundTrip(t *testing.T) {
	s := New()
	const name = "test-big-value"
	defer s.Delete(name)

	want := strings.Repeat("сервер-обхода;", 600) // ~15 КБ
	if err := s.PutJSON(name, want); err != nil {
		t.Skip("хранилище Windows недоступно:", err)
	}
	var got string
	if err := s.GetJSON(name, &got); err != nil || got != want {
		t.Fatalf("прочитано не то: len %d err %v", len(got), err)
	}
	// Маленькое поверх большого — части убраны, читается маленькое.
	if err := s.PutJSON(name, "мало"); err != nil {
		t.Fatal(err)
	}
	if err := s.GetJSON(name, &got); err != nil || got != "мало" {
		t.Fatalf("после перезаписи %q %v", got, err)
	}
}
