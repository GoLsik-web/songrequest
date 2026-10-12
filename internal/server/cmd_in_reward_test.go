package server

import "testing"

// Команда, написанная в награду, — не заказ; обычный текст — заказ.
func TestIsChatCommand(t *testing.T) {
	for text, want := range map[string]bool{
		"!скип":           true,
		" !очередь ":      true,
		"!громкость 40":   true,
		"!queen bohemian": false,
		"скип":            false,
		"Skip Marley":     false,
	} {
		if got := isChatCommand(text, "!"); got != want {
			t.Errorf("%q: ждали %v, вышло %v", text, want, got)
		}
	}
}
