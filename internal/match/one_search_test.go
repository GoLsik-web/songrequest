package match

import (
	"context"
	"testing"
)

// «Артист Название» без тире — самый частый заказ. Точная находка должна
// обходиться одним поиском: 12.10 второй поиск транслитом удвоил расход, и
// Spotify закрыл поиск на шесть с половиной часов.
func TestOneSearchWhenArtistAndTitleNamed(t *testing.T) {
	lib := []Candidate{
		{ID: "q", Title: "Don't Stop Me Now", Artists: []string{"Queen"}, DurationMs: 209000, Popularity: 85},
		{ID: "x", Title: "Now", Artists: []string{"Someone"}, DurationMs: 200000, Popularity: 10},
	}
	sp := &fakeSpotify{library: lib}
	res, err := Find(context.Background(), sp, Parse("Queen Don't Stop Me Now"), DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Found || res.Track.ID != "q" {
		t.Fatalf("нашлось не то: %+v", res.Track)
	}
	if len(sp.queries) != 1 {
		t.Fatalf("поисков %d, ждали один: %v", len(sp.queries), sp.queries)
	}
}
