package match

import (
	"context"
	"testing"
)

// Каталог у Spotify свой в каждой стране. Трек, не изданный в стране
// аккаунта, играть нельзя — Spotify откажет уже при попытке включить.
// Ставить такой в очередь значит гарантированно сорвать заказ.
func TestTrackUnavailableInAccountCountryIsNotOffered(t *testing.T) {
	found := Candidate{
		ID: "ru1", URI: "spotify:track:ru1",
		Title: "Пыль", Artists: []string{"Сплин"}, DurationMs: 200000,
		Popularity: 60,
		Markets:    []string{"RU", "UA", "KZ"},
	}

	res, err := Find(context.Background(), fixedSearcher{found},
		Parse("Сплин - Пыль"), Options{Accept: 0.8, Maybe: 0.55, Market: "IN",
			Weights: DefaultWeights()})
	if err != nil {
		t.Fatal(err)
	}

	if res.Found {
		t.Fatal("трек, не изданный в стране аккаунта, не должен попадать в очередь")
	}
	if res.AbroadOnly == 0 {
		t.Fatal("причина отказа должна быть видна: иначе это неотличимо от «нет в Spotify»")
	}
}

// Тот же трек в подходящей стране обязан находиться.
func TestSameTrackIsFoundWhereItIsAvailable(t *testing.T) {
	found := Candidate{
		ID: "ru1", URI: "spotify:track:ru1",
		Title: "Пыль", Artists: []string{"Сплин"}, DurationMs: 200000,
		Popularity: 60,
		Markets:    []string{"RU", "UA", "KZ"},
	}

	res, err := Find(context.Background(), fixedSearcher{found},
		Parse("Сплин - Пыль"), Options{Accept: 0.8, Maybe: 0.55, Market: "RU",
			Weights: DefaultWeights()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Found {
		t.Fatalf("в своей стране трек обязан находиться, оценка %.2f", res.Score.Total)
	}
}

// Список стран Spotify присылает не всегда. Молча съедать заказ из-за
// отсутствующего поля нельзя — лучше отдать трек, который, возможно,
// не заиграет, чем потерять заказ на пустом месте.
func TestNoMarketListMeansNoFiltering(t *testing.T) {
	found := Candidate{
		ID: "x", URI: "spotify:track:x",
		Title: "Пыль", Artists: []string{"Сплин"}, DurationMs: 200000, Popularity: 60,
	}

	res, err := Find(context.Background(), fixedSearcher{found},
		Parse("Сплин - Пыль"), Options{Accept: 0.8, Maybe: 0.55, Market: "IN",
			Weights: DefaultWeights()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Found {
		t.Fatal("без списка стран отсеивать нечего")
	}
}

// fixedSearcher всегда возвращает один и тот же набор.
type fixedSearcher struct{ one Candidate }

func (f fixedSearcher) SearchTracks(ctx context.Context, q string, limit int) ([]Candidate, error) {
	return []Candidate{f.one}, nil
}
