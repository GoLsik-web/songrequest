package player

import "testing"

// Панель Windows пишет русских артистов кириллицей, Spotify по сети — латиницей.
func TestSamePanelTrackAcrossAlphabets(t *testing.T) {
	cases := []struct {
		panel, order string
	}{
		{"Макс Корж", "Max Korzh"},
		{"SLAVA MARLOW", "SLAVA MARLOW"},
		{"Кино", "Kino"},
	}
	for _, c := range cases {
		if !samePanelTrack(PanelTrack{Title: "Песня", Artist: c.panel}, "Песня", c.order) {
			t.Errorf("%q и %q не признаны одним артистом", c.panel, c.order)
		}
	}
	if samePanelTrack(PanelTrack{Title: "Песня", Artist: "Другой"}, "Песня", "Max Korzh") {
		t.Error("чужой артист с тем же названием признан своим")
	}
}
