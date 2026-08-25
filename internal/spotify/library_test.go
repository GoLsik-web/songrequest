package spotify

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Тестер видит «0 треков» у плейлистов, в которых треки есть. Проверяем весь
// путь числа: от ответа Spotify до структуры, которая уходит в панель.
func TestPlaylistTrackCountSurvivesParsing(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Ответ в том виде, в каком его отдаёт Spotify.
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"items": [
				{"id":"a1","name":"my likess","uri":"spotify:playlist:a1",
				 "owner":{"display_name":"tester"},
				 "tracks":{"href":"https://api.spotify.com/v1/playlists/a1/tracks","total":137}},
				{"id":"a2","name":"LOW CORTISOL","uri":"spotify:playlist:a2",
				 "owner":{"display_name":"tester"},
				 "tracks":{"href":"https://api.spotify.com/v1/playlists/a2/tracks","total":42}}
			],
			"next": null
		}`))
	})
	// Право читать плейлисты выдаётся при входе; без него клиент честно
	// отказывается и до разбора ответа дело не доходит.
	c.tokens.Scope = strings.Join(scopes, " ")

	list, err := c.Playlists(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("плейлистов должно быть два, пришло %d", len(list))
	}
	if list[0].Tracks != 137 || list[1].Tracks != 42 {
		t.Fatalf("число треков потерялось: %d и %d", list[0].Tracks, list[1].Tracks)
	}
	if list[0].Name != "my likess" {
		t.Fatalf("имя потерялось: %q", list[0].Name)
	}
}

// Spotify иногда отдаёт в общем списке ноль треков у непустых плейлистов.
// Тогда число надо переспросить у самого плейлиста, а не показывать ноль:
// стример по этому числу выбирает запасной плейлист.
func TestZeroTrackCountIsAskedAgain(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(r.URL.Path, "/tracks") {
			// Так отвечает сам плейлист — здесь число настоящее.
			w.Write([]byte(`{"total": 137, "items": []}`))
			return
		}
		// А так — общий список: у одного ноль, хотя треки в нём есть.
		w.Write([]byte(`{"items":[
			{"id":"a1","name":"LOW CORTISOL","uri":"spotify:playlist:a1",
			 "owner":{"display_name":"tester"},"tracks":{"total":0}},
			{"id":"a2","name":"полный","uri":"spotify:playlist:a2",
			 "owner":{"display_name":"tester"},"tracks":{"total":42}}
		],"next":null}`))
	})
	c.tokens.Scope = strings.Join(scopes, " ")

	list, err := c.Playlists(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Tracks != 137 {
		t.Fatalf("ноль не переспросили: %d", list[0].Tracks)
	}
	if list[1].Tracks != 42 {
		t.Fatalf("нормальное число испортили: %d", list[1].Tracks)
	}
}
