//go:build !windows

package spotifyapp

// На не-Windows читать нечего: приложение живёт на Windows, а тесты остального
// кода должны собираться везде.
func Read() (Track, Status) { return Track{}, Unknown }
