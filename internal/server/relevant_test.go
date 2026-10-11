package server

import (
	"testing"

	"songrequest/internal/youtube"
)

// 11.10: опечатка и минус в заказе давали чужой ролик.
func TestYouTubeRelevance(t *testing.T) {
	cases := []struct {
		q, title, artist string
		want             bool
	}{
		{"я в деле -slawa merlow", "Лучший PvP игрок. Крупнейшая история обмана", "Kasspov", false},
		{"я в деле slava merlow", "SLAVA MARLOW — Я в деле", "SLAVA MARLOW", true},
		{"Я в деле - SLAWA MARLOW", "Я в деле", "SLAVA MARLOW", true},
		{"кино группа крови", "Группа крови", "КИНО", true},
	}
	for _, c := range cases {
		got := relevant(c.q, &youtube.Track{Title: c.title, Artist: c.artist})
		if got != c.want {
			t.Errorf("%q → %q: %v, ждали %v", c.q, c.title, got, c.want)
		}
	}
	if q := youtube.CleanQuery(`я в деле -slawa "merlow"`); q != "я в деле slawa merlow" {
		t.Errorf("CleanQuery: %q", q)
	}
}
