package telegram

import "strings"

// SplitText сохраняет весь отчёт. Считаем UTF-16 единицы, чтобы emoji
// тоже укладывались в лимит Telegram. Предпочитаем границу строки.
func SplitText(text string) []string {
	const limit = 3800
	var parts []string
	for text != "" {
		units, end := 0, 0
		for index, r := range text {
			cost := 1
			if r > 0xffff {
				cost = 2
			}
			if units+cost > limit {
				end = index
				break
			}
			units += cost
			end = len(text)
		}
		if end < len(text) {
			if newline := strings.LastIndex(text[:end], "\n"); newline > 0 {
				end = newline + 1
			}
		}
		parts = append(parts, text[:end])
		text = text[end:]
	}
	return parts
}
