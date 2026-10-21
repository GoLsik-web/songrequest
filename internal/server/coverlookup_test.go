package server

import "context"

// Проверки не ходят в каталоги обложек: сеть в них медленная и
// непредсказуемая, а проверяется не она.
func init() {
	coverLookup = func(context.Context, string, string) string { return "" }
}
