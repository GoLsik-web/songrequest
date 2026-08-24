// Package player проигрывает очередь заказов и возвращает Spotify на место.
//
// Опроса здесь нет. Длительность трека известна заранее, поэтому приложение
// просто спит до его конца и один раз проверяет, что трек действительно
// доиграл. На заказ уходит два запроса к Spotify вместо сотни, а в простое
// горутина спит на канале и не тратит процессор.
package player

import (
	"context"
	"sync"
	"time"

	"songrequest/internal/errs"
	"songrequest/internal/logx"
	"songrequest/internal/queue"
	"songrequest/internal/spotify"
)

// YouTube — запасной проигрыватель для того, чего нет в Spotify.
// Интерфейсом, чтобы плеер не зависел от yt-dlp и mpv в тестах.
type YouTube interface {
	Play(ctx context.Context, url string) error
	Wait(ctx context.Context) bool
	Stop()
}

// Spotify — то, что умеет играть. Интерфейсом ради тестов.
type Spotify interface {
	Capture(ctx context.Context) (*spotify.Snapshot, error)
	Restore(ctx context.Context, snap *spotify.Snapshot, playedURI string, force bool) (spotify.RestoreOutcome, error)
	PlayTrack(ctx context.Context, trackURI, deviceID string) error
	Pause(ctx context.Context, deviceID string) error
	State(ctx context.Context) (*spotify.PlayerState, bool, error)
}

// Now — что играет прямо сейчас.
type Now struct {
	Item      queue.Item
	StartedAt time.Time
}

// Elapsed — сколько уже отыграно.
func (n *Now) Elapsed() time.Duration {
	if n == nil {
		return 0
	}
	return time.Since(n.StartedAt)
}

// Player проигрывает очередь.
type Player struct {
	q       *queue.Queue
	spotify Spotify
	youtube YouTube
	log     *logx.Logger

	// ResumeDelay — пауза перед возвратом. Заказы часто идут подряд, и
	// возвращать музыку между ними, чтобы через секунду снова прервать, —
	// худшее, что можно сделать со звуком на стриме.
	ResumeDelay time.Duration

	// Обратные вызовы наружу: пакет не знает ни про панель, ни про Twitch.
	OnChange   func()
	OnFinished func(item queue.Item)
	OnRestored func(outcome spotify.RestoreOutcome, snap *spotify.Snapshot)
	OnError    func(err error)

	mu      sync.Mutex
	now     *Now
	snap    *spotify.Snapshot
	paused  bool
	wake    chan struct{}
	skip    chan struct{}
	running bool
}

// SetYouTube подключает запасной проигрыватель.
func (p *Player) SetYouTube(y YouTube) {
	p.mu.Lock()
	p.youtube = y
	p.mu.Unlock()
}

// New создаёт плеер.
func New(q *queue.Queue, sp Spotify, log *logx.Logger) *Player {
	return &Player{
		q:           q,
		spotify:     sp,
		log:         log,
		ResumeDelay: 3 * time.Second,
		wake:        make(chan struct{}, 1),
		skip:        make(chan struct{}, 1),
	}
}

// Now отдаёт текущий заказ.
func (p *Player) Now() *Now {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.now
}

// Snapshot отдаёт запомненное состояние Spotify.
func (p *Player) Snapshot() *spotify.Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snap
}

// SetSnapshot подменяет снимок — например, когда стример нажал «запомнить».
func (p *Player) SetSnapshot(s *spotify.Snapshot) {
	p.mu.Lock()
	p.snap = s
	p.mu.Unlock()
	p.changed()
}

// Nudge будит плеер: в очередь что-то добавили.
func (p *Player) Nudge() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Skip обрывает текущий трек.
func (p *Player) Skip() {
	select {
	case p.skip <- struct{}{}:
	default:
	}
}

// SetPaused ставит воспроизведение заказов на паузу. Уже играющий трек
// доигрывает: обрывать его на полуслове — не то, что имеют в виду.
func (p *Player) SetPaused(v bool) {
	p.mu.Lock()
	p.paused = v
	p.mu.Unlock()
	if !v {
		p.Nudge()
	}
	p.changed()
}

// Paused сообщает, стоит ли воспроизведение на паузе.
func (p *Player) Paused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paused
}

// Run крутит очередь до отмены контекста.
func (p *Player) Run(ctx context.Context) {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	p.mu.Unlock()

	for ctx.Err() == nil {
		item, err := p.take()
		if err != nil {
			// Очередь пуста — ждём, пока что-нибудь появится.
			p.idle(ctx)
			continue
		}
		p.playOne(ctx, item)
	}
}

// take забирает следующий заказ, если можно играть.
func (p *Player) take() (queue.Item, error) {
	if p.Paused() {
		return queue.Item{}, queue.ErrEmpty
	}
	return p.q.Next()
}

// idle ждёт нового заказа и по дороге возвращает Spotify на место.
func (p *Player) idle(ctx context.Context) {
	// Возвращаем, только когда очередь действительно пуста, и с задержкой:
	// следующий заказ может прийти через секунду.
	if p.Snapshot() != nil && p.Now() == nil {
		if !sleep(ctx, p.ResumeDelay) {
			return
		}
		if n, _ := p.q.Len(); n == 0 && !p.Paused() {
			p.restore(ctx)
		}
	}

	select {
	case <-ctx.Done():
	case <-p.wake:
	case <-p.skip: // скип на пустой очереди — просто будим
	case <-time.After(30 * time.Second):
		// Редкая подстраховка на случай, если сигнал потерялся: раз в
		// полминуты заглядываем в очередь сами.
	}
}

// playOne проигрывает один заказ от начала до конца.
func (p *Player) playOne(ctx context.Context, item queue.Item) {
	// Первый заказ подряд — запоминаем, куда возвращаться.
	if p.Snapshot() == nil {
		snap, err := p.spotify.Capture(ctx)
		if err != nil {
			p.fail(err)
			// Без снимка играть можно, но вернуться потом будет некуда —
			// об этом уже сказано в панели.
		} else {
			p.SetSnapshot(snap)
		}
	}

	// Заказ с YouTube играется иначе: Spotify ставится на паузу, звук идёт
	// отдельной программой на отдельное устройство.
	if item.Provider == "youtube" {
		if err := p.playYouTube(ctx, item); err != nil {
			p.fail(err)
			p.finish(item)
			return
		}
	} else if err := p.spotify.PlayTrack(ctx, item.URI, ""); err != nil {
		p.log.Error("не смог включить заказ",
			"трек", item.Artist+" — "+item.Title, "ошибка", err)
		p.fail(err)
		p.finish(item)
		return
	}

	p.mu.Lock()
	p.now = &Now{Item: item, StartedAt: time.Now()}
	p.mu.Unlock()
	p.changed()

	p.log.Info("играет заказ",
		"трек", item.Artist+" — "+item.Title, "заказал", item.Requester,
		"откуда", item.Provider, "длительность_мс", item.DurationMs)

	if item.Provider == "youtube" {
		p.awaitYouTube(ctx, item)
	} else {
		p.await(ctx, item)
	}

	p.mu.Lock()
	p.now = nil
	p.mu.Unlock()
	p.changed()

	p.finish(item)
}

// playYouTube ставит Spotify на паузу и запускает звук с YouTube.
func (p *Player) playYouTube(ctx context.Context, item queue.Item) error {
	p.mu.Lock()
	yt := p.youtube
	p.mu.Unlock()

	if yt == nil {
		return errs.New(errs.YouTubeNoTool, "Проигрыватель YouTube не готов.")
	}

	// Spotify обязательно на паузу: иначе два трека заиграют одновременно.
	if err := p.spotify.Pause(ctx, ""); err != nil {
		p.log.Warn("не поставил Spotify на паузу перед заказом с YouTube", "ошибка", err)
	}
	return yt.Play(ctx, item.URI)
}

// awaitYouTube ждёт, пока mpv доиграет, и убивает его после.
func (p *Player) awaitYouTube(ctx context.Context, item queue.Item) {
	p.mu.Lock()
	yt := p.youtube
	p.mu.Unlock()
	if yt == nil {
		return
	}

	// mpv не должен пережить трек: висящий процесс занимает звуковое
	// устройство, и следующий заказ окажется без звука.
	defer yt.Stop()

	done := make(chan struct{})
	go func() {
		yt.Wait(ctx)
		close(done)
	}()

	select {
	case <-ctx.Done():
	case <-p.skip:
		p.log.Info("заказ с YouTube скипнут", "трек", item.Title)
	case <-done:
	}
}

// await ждёт конца трека.
//
// Спим до расчётного конца, потом один раз спрашиваем Spotify. Если трек
// ещё играет — стример поставил паузу или перемотал назад, — ждём остаток.
func (p *Player) await(ctx context.Context, item queue.Item) {
	left := time.Duration(item.DurationMs) * time.Millisecond

	for attempt := 0; attempt < 10; attempt++ {
		if left < time.Second {
			left = time.Second
		}

		select {
		case <-ctx.Done():
			return
		case <-p.skip:
			p.log.Info("заказ скипнут", "трек", item.Title)
			return
		case <-time.After(left):
		}

		st, playing, err := p.spotify.State(ctx)
		if err != nil {
			p.log.Warn("не проверил, доиграл ли трек", "ошибка", err)
			return
		}
		if !playing || st.Item == nil || st.Item.URI != item.URI {
			return // трек доиграл или его переключили
		}

		// Трек всё ещё наш: ждём остаток и проверяем ещё раз.
		left = time.Duration(st.Item.DurationMs-st.ProgressMs) * time.Millisecond
		if !st.IsPlaying {
			// Стример поставил паузу — ждём его, а не крутим проверки.
			left = 15 * time.Second
		}
	}
}

// restore возвращает Spotify туда, где он был до заказов.
func (p *Player) restore(ctx context.Context) {
	snap := p.Snapshot()
	if snap == nil {
		return
	}

	outcome, err := p.spotify.Restore(ctx, snap, "", false)
	if err != nil {
		p.log.Warn("не вернул Spotify на место", "ошибка", err)
		p.fail(err)
	}

	if p.OnRestored != nil {
		p.OnRestored(outcome, snap)
	}

	// Снимок отработал: держать его дальше нельзя, иначе следующая пачка
	// заказов вернёт музыку на вчерашнее место.
	p.mu.Lock()
	p.snap = nil
	p.mu.Unlock()
	p.changed()
}

func (p *Player) finish(item queue.Item) {
	if p.OnFinished != nil {
		p.OnFinished(item)
	}
}

func (p *Player) changed() {
	if p.OnChange != nil {
		p.OnChange()
	}
}

func (p *Player) fail(err error) {
	if err == nil {
		return
	}
	if p.OnError != nil {
		p.OnError(err)
	}
}

// sleep ждёт d; false означает, что приложение закрывают.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
