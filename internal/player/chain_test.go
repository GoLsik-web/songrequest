package player

import (
	"context"
	"testing"
	"time"

	"songrequest/internal/spotify"
)

// Жалоба тестера 26.08: «с плейлиста на заказ переключает, а с заказного
// трека на заказной — не может». Проверяем ровно это: два заказа подряд.
func TestSecondOrderStartsRightAfterFirst(t *testing.T) {
	p, q, sp := newPlayer(t)
	p.WaitForCurrent = true
	// У стримера играет свой плейлист.
	sp.setPlaying("spotify:track:плейлист", 2000, 1000)

	addTrack(t, q, "первый", 2000)
	addTrack(t, q, "второй", 2000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, "первый заказ пошёл", func() bool {
		played, _, _ := sp.stats()
		return len(played) >= 1
	})

	// Первый заказ доиграл, и Spotify поехал на автоподбор.
	sp.setPlaying("spotify:track:автоподбор", 200000, 0)

	waitFor(t, "второй заказ пошёл", func() bool {
		played, _, _ := sp.stats()
		return len(played) >= 2
	})

	played, _, _ := sp.stats()
	if played[1] != "spotify:track:второй" {
		t.Fatalf("вторым заиграл %q", played[1])
	}
}

// То же самое, но первый заказ скипнули.
func TestSecondOrderStartsAfterSkip(t *testing.T) {
	p, q, sp := newPlayer(t)
	p.WaitForCurrent = true
	sp.setPlaying("spotify:track:плейлист", 2000, 1000)

	addTrack(t, q, "первый", 600000)
	addTrack(t, q, "второй", 2000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, "первый заказ пошёл", func() bool {
		played, _, _ := sp.stats()
		return len(played) >= 1
	})

	time.Sleep(50 * time.Millisecond)
	p.Skip()

	waitFor(t, "второй заказ пошёл", func() bool {
		played, _, _ := sp.stats()
		return len(played) >= 2
	})
}

// Заказ обязан играть на том же устройстве, где играла музыка стримера:
// оттуда идёт звук в эфир. «Где активно сейчас» после доигравшего заказа не
// значит ничего — активным не остаётся ни одно устройство.
func TestOrderPlaysOnStreamerDevice(t *testing.T) {
	p, q, sp := newPlayer(t)
	sp.snapshot = &spotify.Snapshot{
		TrackURI: "spotify:track:исходный", TrackName: "Исходный",
		ContextURI: "spotify:playlist:любимое", DeviceID: "комп-стримера",
		PositionMs: 30000, IsPlaying: true,
	}

	addTrack(t, q, "заказ", 60000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, "заказ пошёл", func() bool {
		played, _, _ := sp.stats()
		return len(played) >= 1
	})
	if got := sp.lastDevice(); got != "комп-стримера" {
		t.Fatalf("заказ ушёл на устройство %q, а звук в эфире идёт с «комп-стримера»", got)
	}
}
