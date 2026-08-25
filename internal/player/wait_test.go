package player

import (
	"context"
	"testing"
	"time"
)

// Главное правило после жалобы стримера: заказ не обрывает то, что играет
// у него самого. Он ждёт конца трека.
func TestOrderWaitsForCurrentTrackToEnd(t *testing.T) {
	p, q, sp := newPlayer(t)
	p.WaitForCurrent = true

	// У стримера играет трек, до конца полторы секунды.
	sp.setPlaying("spotify:track:своё", 200000, 198500)
	addTrack(t, q, "заказ", 60000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	// Сразу включать нельзя: чужой трек ещё звучит.
	time.Sleep(400 * time.Millisecond)
	if playedURIs(sp)[0] != "" {
		t.Fatalf("заказ включился, не дождавшись конца трека: %v", playedURIs(sp))
	}

	// Трек доиграл, и Spotify сам перешёл к следующему в плейлисте.
	sp.setPlaying("spotify:track:следующий", 190000, 400)

	waitUntil(t, 6*time.Second, "заказ так и не заиграл", func() bool {
		return playedURIs(sp)[0] == "spotify:track:заказ"
	})
}

// Снимок делается ПОСЛЕ ожидания, а не в момент заказа. Иначе возврат
// приведёт на трек, который зрители только что дослушали целиком, и он
// заиграет второй раз подряд.
func TestSnapshotIsTakenAfterWaiting(t *testing.T) {
	p, q, sp := newPlayer(t)
	p.WaitForCurrent = true

	sp.setPlaying("spotify:track:своё", 200000, 199000)
	addTrack(t, q, "заказ", 60000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	time.Sleep(300 * time.Millisecond)
	if _, captured, _ := sp.stats(); captured != 0 {
		t.Fatal("снимок сделан до ожидания: возврат приведёт на уже отыгравший трек")
	}

	sp.setPlaying("spotify:track:следующий", 190000, 400)
	waitUntil(t, 6*time.Second, "снимок так и не сделан", func() bool {
		_, captured, _ := sp.stats()
		return captured > 0
	})
}

// Ждать нужно не всегда: выключенная настройка означает «включай сразу».
func TestWaitingCanBeTurnedOff(t *testing.T) {
	p, q, sp := newPlayer(t)
	p.WaitForCurrent = false

	// Играет долгий трек: до конца больше трёх минут.
	sp.setPlaying("spotify:track:своё", 200000, 1000)
	addTrack(t, q, "заказ", 60000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitUntil(t, 3*time.Second, "с выключенным ожиданием заказ обязан играть сразу",
		func() bool { return playedURIs(sp)[0] == "spotify:track:заказ" })
}

// Скип во время ожидания означает «не жди, включай». Другой кнопки для этого
// у стримера нет, и молча пропадать она не должна.
func TestSkipDuringWaitStartsOrderNow(t *testing.T) {
	p, q, sp := newPlayer(t)
	p.WaitForCurrent = true

	sp.setPlaying("spotify:track:своё", 600000, 1000) // до конца десять минут
	addTrack(t, q, "заказ", 60000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	time.Sleep(300 * time.Millisecond)
	p.Skip()

	waitUntil(t, 3*time.Second, "скип во время ожидания не включил заказ",
		func() bool { return playedURIs(sp)[0] == "spotify:track:заказ" })
}

// Тишина у стримера — ждать нечего, заказ играет сразу.
func TestNothingPlayingMeansNoWaiting(t *testing.T) {
	p, q, sp := newPlayer(t)
	p.WaitForCurrent = true

	addTrack(t, q, "заказ", 60000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitUntil(t, 3*time.Second, "на тишине ждать нечего, заказ обязан играть сразу",
		func() bool { return playedURIs(sp)[0] == "spotify:track:заказ" })
}

// playedURIs всегда возвращает непустой срез, чтобы проверки читались как
// одна строка, а не как три с проверкой длины.
func playedURIs(sp *fakeSpotify) []string {
	played, _, _ := sp.stats()
	if len(played) == 0 {
		return []string{""}
	}
	return played
}

func waitUntil(t *testing.T, limit time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(what)
}

// Стример может скипнуть заказ прямо в Spotify, мимо панели. Раньше
// приложение узнавало об этом последним: оно спало всю длительность трека и
// показывало в панели давно замолчавший заказ ещё несколько минут.
func TestOrderSkippedInSpotifyEndsRightAway(t *testing.T) {
	p, q, sp := newPlayer(t)
	addTrack(t, q, "заказ", 240000) // четыре минуты

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitUntil(t, 3*time.Second, "заказ не заиграл",
		func() bool { return p.Now() != nil })

	// Стример переключил трек в самом Spotify.
	sp.finish()

	waitUntil(t, 3*time.Second, "приложение не заметило, что заказ выключили",
		func() bool { return p.Now() == nil })
}

// Перемотка внутри заказа должна доезжать до панели: иначе время на полосе
// врёт до самого конца трека.
func TestSeekInsideOrderMovesThePosition(t *testing.T) {
	p, q, sp := newPlayer(t)
	addTrack(t, q, "заказ", 240000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitUntil(t, 3*time.Second, "заказ не заиграл",
		func() bool { return p.Now() != nil })

	// Стример перемотал на третью минуту.
	sp.setPlaying("spotify:track:заказ", 240000, 180000)

	waitUntil(t, 5*time.Second, "перемотка не доехала до панели", func() bool {
		now := p.Now()
		return now != nil && now.Elapsed() > 170*time.Second
	})
}

// Очередь обязана двигаться сама. Даже в худшем случае — когда Spotify
// упорно показывает всё тот же трек и никогда не говорит, что он кончился, —
// следующий заказ должен заиграть, а не ждать, пока стример нажмёт скип.
func TestQueueMovesOnEvenIfSpotifyNeverSaysTheTrackEnded(t *testing.T) {
	p, q, sp := newPlayer(t)
	// Spotify будет вечно отвечать «играет тот же трек, позиция ноль».
	sp.setPlaying("spotify:track:первый", 400, 0)

	addTrack(t, q, "первый", 400)
	addTrack(t, q, "второй", 400)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitUntil(t, 20*time.Second, "второй заказ так и не заиграл сам", func() bool {
		played, _, _ := sp.stats()
		return len(played) >= 2 && played[1] == "spotify:track:второй"
	})
}

// Пауза у самого конца трека — это конец, а не пауза: Spotify так показывает
// доигравшую запись, и ждать там нечего.
func TestPausedAtTheEndCountsAsFinished(t *testing.T) {
	p, q, sp := newPlayer(t)
	addTrack(t, q, "первый", 200000)
	addTrack(t, q, "второй", 200000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitUntil(t, 3*time.Second, "первый не заиграл",
		func() bool { return p.Now() != nil })

	// Трек доигран и стоит в конце.
	sp.pause("spotify:track:первый", 200000, 199900)

	waitUntil(t, 5*time.Second, "второй заказ не пошёл после доигравшего первого",
		func() bool {
			played, _, _ := sp.stats()
			return len(played) >= 2
		})
}
