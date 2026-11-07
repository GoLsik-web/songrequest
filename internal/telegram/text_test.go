package telegram

import (
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

func TestLongReportKeepsEveryLineAndEmoji(t *testing.T) {
	for _, text := range []string{strings.Repeat("🎵 Длинный отчёт\n", 800), strings.Repeat("🖼", 3000), strings.Repeat("я", 9000)} {
		parts := SplitText(text)
		if strings.Join(parts, "") != text {
			t.Fatal("report text lost")
		}
		for _, part := range parts {
			if !utf8.ValidString(part) || len(utf16.Encode([]rune(part))) > 3800 || part == "" {
				t.Fatal("invalid message boundary")
			}
		}
	}
}
