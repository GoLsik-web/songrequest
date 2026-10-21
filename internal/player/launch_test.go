package player

import (
	"context"
	"sync"
	"testing"
	"time"

	"songrequest/internal/queue"
)

// slowYouTube качает звук долго — как через обход — и отпускает по команде
// теста или по отмене.
type slowYouTube struct {
	*fakeYouTube
	release chan struct{}
	mu      sync.Mutex
	started bool
}

func (s *slowYouTube) Prefetch(ctx context.Context, url string) error {
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *slowYouTube) prefetching() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

// Пока качается звук, заказ виден как «запускается», Spotify ещё не на паузе,
// а скип отменяет запуск и засчитывается как скип (не как «не получилось»).
func TestSkipDuringLaunchCancelsIt(t *testing.T) {
	p, q, sp := newPlayer(t)
	yt := &slowYouTube{fakeYouTube: newFakeYouTube(), release: make(chan struct{})}
	p.SetYouTube(yt)

	var mu sync.Mutex
	var finished []bool
	var dropped int
	p.OnFinished = func(item queue.Item, natural bool) {
		mu.Lock()
		finished = append(finished, natural)
		mu.Unlock()
	}
	p.OnDropped = func(item queue.Item, err error) {
		mu.Lock()
		dropped++
		mu.Unlock()
	}

	addYouTubeTrack(t, q, "долгий", 600000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	waitFor(t, "началось скачивание", yt.prefetching)
	if p.LaunchingItem() == nil || !p.Waiting() || p.Now() != nil {
		t.Fatal("во время скачивания заказ не виден как «запускается»")
	}
	sp.mu.Lock()
	held := sp.held
	sp.mu.Unlock()
	if held {
		t.Fatal("Spotify поставлен на паузу до того, как звук скачался")
	}

	p.Skip()

	waitFor(t, "запуск отменён скипом", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(finished) == 1
	})
	mu.Lock()
	defer mu.Unlock()
	if finished[0] || dropped != 0 {
		t.Fatalf("скип при запуске засчитан неверно: natural=%v dropped=%d", finished[0], dropped)
	}
	if p.LaunchingItem() != nil {
		t.Fatal("признак «запускается» остался после отмены")
	}
	played, _ := yt.stats()
	if len(played) != 0 {
		t.Fatal("ролик всё равно включился после скипа")
	}
}

// Запуск, который не уложился в срок, не держит очередь вечно.
func TestLaunchHasDeadline(t *testing.T) {
	p, q, _ := newPlayer(t)
	p.launchLimit = 100 * time.Millisecond
	yt := &slowYouTube{fakeYouTube: newFakeYouTube(), release: make(chan struct{})}
	p.SetYouTube(yt)

	done := make(chan struct{}, 1)
	p.OnDropped = func(item queue.Item, err error) { done <- struct{}{} }

	addYouTubeTrack(t, q, "вечный", 600000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("зависший запуск не снят по сроку")
	}
}
