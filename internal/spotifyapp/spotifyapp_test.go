package spotifyapp

import "testing"

// Заголовок окна Spotify — единственный источник, который работает всегда:
// без сети, без ключа доступа и без нормы запросов. Разбирать его надо
// осторожно: в кадре у стримера окажется ровно то, что мы отсюда достанем.
func TestParseTitle(t *testing.T) {
	tests := []struct {
		title  string
		artist string
		name   string
		want   Status
	}{
		{"Hayd - Closure", "Hayd", "Closure", Playing},
		{"Avril Lavigne - Sk8er Boi - Remastered", "Avril Lavigne", "Sk8er Boi - Remastered", Playing},
		{"Би-2 - Полковнику никто не пишет", "Би-2", "Полковнику никто не пишет", Playing},

		// Музыка не играет — заголовок у Spotify простой. Это именно «стоит»,
		// а не «непонятно»: спрашивать Spotify по сети незачем.
		{"Spotify", "", "", Idle},
		{"Spotify Premium", "", "", Idle},
		{"Spotify Free", "", "", Idle},
		{"spotify premium", "", "", Idle},

		// Чужое или непонятное — пусть решает опрос по сети.
		{"", "", "", Unknown},
		{"Просто окно", "", "", Unknown},
		{" - Closure", "", "", Unknown},
		{"Hayd - ", "", "", Unknown},
	}

	for _, tt := range tests {
		got, status := parseTitle(tt.title)
		if status != tt.want {
			t.Errorf("%q: состояние %v, а ждали %v", tt.title, status, tt.want)
			continue
		}
		if status != Playing {
			continue
		}
		if got.Artist != tt.artist || got.Title != tt.name {
			t.Errorf("%q: вышло %q — %q, а ждали %q — %q",
				tt.title, got.Artist, got.Title, tt.artist, tt.name)
		}
	}
}

func TestSame(t *testing.T) {
	a := Track{Artist: "Hayd", Title: "Closure"}
	if !a.Same(Track{Artist: "Hayd", Title: "Closure"}) {
		t.Error("один и тот же трек посчитали разным")
	}
	if a.Same(Track{Artist: "Hayd", Title: "Numb"}) {
		t.Error("разные треки посчитали одним")
	}
}
