package match

import "testing"

// 12.10: пометка «ft.» без скобок съедала название трека целиком.
func TestFeatBareKeepsTitle(t *testing.T) {
	cases := []struct{ in, artist, title, feat string }{
		{"The Weeknd ft. Daft Punk - I Feel It Coming", "The Weeknd", "I Feel It Coming", "Daft Punk"},
		{"Eminem feat. Rihanna — Love The Way You Lie", "Eminem", "Love The Way You Lie", "Rihanna"},
		{"Drake - One Dance (feat. Wizkid)", "Drake", "One Dance", "Wizkid"},
		{"Billy Idol - Dancing With Myself", "Billy Idol", "Dancing With Myself", ""},
	}
	for _, c := range cases {
		r := Parse(c.in)
		if r.Artist != c.artist || r.Title != c.title {
			t.Errorf("%q: артист %q, название %q", c.in, r.Artist, r.Title)
		}
		if c.feat != "" && (len(r.Feats) == 0 || r.Feats[0] != c.feat) {
			t.Errorf("%q: доп. артисты %v", c.in, r.Feats)
		}
	}
}
