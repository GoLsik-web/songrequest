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

	// PollEvery — шаг проверки Spotify, пока играет заказ. Вынесен наружу
	// ради тестов: они не должны ждать живые шесть секунд, чтобы проверить,
	// что перемотка доезжает.
	PollEvery time.Duration

	// WaitForCurrent — дожидаться конца трека, который играет у стримера,
	// прежде чем включить первый заказ.
	WaitForCurrent bool

	// Настройки ниже читает горутина очереди, а меняет их панель. Только
	// через SetOptions: голое присваивание — гонка.

	// OnDropped — заказ не удалось включить: баллы надо вернуть, а зрителю
	// сказать. Без этого списанные баллы просто пропадают.
	OnDropped func(item queue.Item, err error)

	// ResumeDelay — пауза перед возвратом. Заказы часто идут подряд, и
	// возвращать музыку между ними, чтобы через секунду снова прервать, —
	// худшее, что можно сделать со звуком на стриме.
	ResumeDelay time.Duration

	// OnWaiting — заказ не заиграл сразу, а ждёт конца трека стримера.
	// Второй аргумент — сколько ждать. Без этого ожидание выглядит как
	// зависание: заказ принят, а музыка не меняется несколько минут.
	OnWaiting func(item queue.Item, left time.Duration)

	// Обратные вызовы наружу: пакет не знает ни про панель, ни про Twitch.
	OnChange func()
	// OnFinished — заказ закончился. natural=false означает «оборвали»:
	// скипнули из панели или переключили музыку прямо в Spotify. Отличать
	// это обязательно: оборванный заказ — не «отыгравший».
	OnFinished func(item queue.Item, natural bool)
	OnRestored func(outcome spotify.RestoreOutcome, snap *spotify.Snapshot)
	OnError    func(err error)

	mu  sync.Mutex
	now *Now
	// lastPlayed — URI последнего отыгравшего заказа. Нужен возврату,
	// чтобы отличить «в Spotify наш заказ» от «стример переключил сам».
	lastPlayed string
	// endedNaturally — заказ доиграл сам, а не был оборван. Это разрешение
	// возврату работать без оглядки на то, что сейчас в Spotify: см. restore.
	endedNaturally bool
	// restoreTries — сколько раз возврат подряд сорвался ошибкой. Пока
	// попытки не кончились, снимок держим: место возврата — единственное,
	// что у нас есть, и терять его из-за моргнувшей сети нельзя.
	restoreTries int
	snap         *spotify.Snapshot
	paused       bool
	// waiting — заказ взят, но ждёт конца трека стримера.
	waiting bool
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
		ResumeDelay: time.Second,
		PollEvery:   defaultPoll,
		wake:        make(chan struct{}, 1),
		skip:        make(chan struct{}, 1),
	}
}

// Now отдаёт текущий заказ.
//
// Копией, а не указателем: снаружи Elapsed() читает StartedAt, а перемотка
// двигает это же поле из горутины очереди. Отдавать живую структуру значит
// отдавать гонку.
func (p *Player) Now() *Now {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.now == nil {
		return nil
	}
	n := *p.now
	return &n
}

// Waiting сообщает, что заказ уже взят в работу, но ждёт конца трека
// стримера. Для панели и для чата это «сейчас играет» в смысле «есть что
// торопить скипом».
func (p *Player) Waiting() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waiting
}

func (p *Player) setWaiting(v bool) {
	p.mu.Lock()
	p.waiting = v
	p.mu.Unlock()
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
		delay, _ := p.options()
		if !sleep(ctx, delay) {
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
	// Скип, нажатый между заказами — пока шёл запуск, снимок или возврат, —
	// оставался лежать в канале и обрывал следующий заказ в первую же
	// секунду. Со стороны выглядело как «заказ пропал сам».
	p.drainSkip()

	// Первый заказ подряд не должен обрывать то, что стример слушает: ждём,
	// пока трек доиграет сам. Снимок делается уже после ожидания — тогда
	// Spotify успевает перевести стрелку на следующий трек плейлиста, и
	// возврат приведёт туда, где музыка и была бы без заказа.
	if p.Snapshot() == nil {
		// Пока идёт ожидание, наружу надо показывать, что заказ уже в работе.
		// Без этого «Скип» в панели и !скип в чате молчали: оба начинаются с
		// проверки Now()!=nil, а во время ожидания Now() ещё пуст. Стример
		// видел «заиграет примерно через три минуты» и не мог это ускорить
		// ничем — при том что сам скип ожидание прерывать умеет.
		p.setWaiting(true)
		p.waitForOwnTrack(ctx, item)
		p.setWaiting(false)
		if ctx.Err() != nil {
			return
		}
	}

	// Первый заказ подряд — запоминаем, куда возвращаться.
	if p.Snapshot() == nil {
		snap, err := p.spotify.Capture(ctx)
		if err != nil {
			p.fail(err)
			// Без снимка играть можно, но вернуться потом будет некуда —
			// об этом уже сказано в панели.
		} else {
			p.SetSnapshot(p.notOurOwn(snap))
		}
	}

	// Заказ с YouTube играется иначе: Spotify ставится на паузу, звук идёт
	// отдельной программой на отдельное устройство.
	if item.Provider == "youtube" {
		if err := p.playYouTube(ctx, item); err != nil {
			p.fail(err)
			p.dropped(item, err)
			return
		}
	} else if err := p.spotify.PlayTrack(ctx, item.URI, p.playDevice()); err != nil {
		p.log.Error("не смог включить заказ",
			"трек", item.Artist+" — "+item.Title, "ошибка", err)
		p.fail(err)
		p.dropped(item, err)
		return
	}

	p.mu.Lock()
	p.now = &Now{Item: item, StartedAt: time.Now()}
	p.mu.Unlock()
	p.changed()

	p.log.Info("играет заказ",
		"трек", item.Artist+" — "+item.Title, "заказал", item.Requester,
		"откуда", item.Provider, "длительность_мс", item.DurationMs)

	natural := false
	if item.Provider == "youtube" {
		natural = p.awaitYouTube(ctx, item)
	} else {
		natural = p.await(ctx, item)
	}

	p.mu.Lock()
	p.now = nil
	// Нужен возврату: он должен отличать «в Spotify наш отыгравший заказ» от
	// «стример переключил музыку сам».
	p.lastPlayed = item.URI
	p.endedNaturally = natural
	p.mu.Unlock()
	p.changed()

	p.finish(item, natural)
}

// playDevice — где играть заказ.
//
// Это то же устройство, на котором играла музыка стримера в момент снимка:
// именно оттуда идёт звук в эфир. Без явного указания Spotify играет «где
// активно сейчас», а после доигравшего заказа активным не остаётся ничего —
// и заказ либо не включался вовсе, либо мог уехать на телефон, который у
// стримера тоже залогинен.
//
// Пустая строка означает «решай сам»: снимка ещё нет, значит и музыки не
// было, и выбирать не из чего.
func (p *Player) playDevice() string {
	snap := p.Snapshot()
	if snap == nil {
		return ""
	}
	return snap.DeviceID
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
// true означает «доиграл сам», а не «оборвали».
func (p *Player) awaitYouTube(ctx context.Context, item queue.Item) bool {
	p.mu.Lock()
	yt := p.youtube
	p.mu.Unlock()
	if yt == nil {
		return false
	}

	// mpv не должен пережить трек: висящий процесс занимает звуковое
	// устройство, и следующий заказ окажется без звука.
	defer yt.Stop()

	// Ответ Wait важен: mpv может упасть через полсекунды после старта, и
	// раньше это засчитывалось как «доиграл». Баллы списаны, зритель ничего
	// не услышал, на Twitch заказ отмечен выполненным.
	done := make(chan bool, 1)
	go func() { done <- yt.Wait(ctx) }()

	// Жёсткий срок, как и у заказов Spotify. Без него зависший mpv (не
	// открылся поток, занято звуковое устройство, yt-dlp внутри ждёт ответа
	// YouTube) держал очередь навсегда: тишина в эфире, панель показывает
	// «играет», новые заказы копятся, и выйти из этого можно только скипом,
	// о котором ещё надо догадаться.
	length := time.Duration(item.DurationMs) * time.Millisecond
	if length <= 0 {
		length = 10 * time.Minute
	}
	limit := time.NewTimer(length + 30*time.Second)
	defer limit.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-p.skip:
		p.log.Info("заказ с YouTube скипнут", "трек", item.Title)
		return false
	case <-limit.C:
		p.log.Warn("заказ с YouTube не кончился в срок — снимаю",
			"трек", item.Title, "длительность_мс", item.DurationMs)
		return false
	case ok := <-done:
		return ok
	}
}

// await ждёт конца трека.
//
// Спим до расчётного конца, потом один раз спрашиваем Spotify. Если трек
// ещё играет — стример поставил паузу или перемотал назад, — ждём остаток.
//
// Возвращает true, если заказ доиграл сам, и false, если его оборвали —
// скипом или руками в самом Spotify. Разница важна возврату: см. restore.
func (p *Player) await(ctx context.Context, item queue.Item) bool {
	// Главное правило: очередь обязана двигаться сама. Что бы ни ответил
	// Spotify — ошибку, молчание, чужой трек, тот же трек на паузе, — заказ
	// заканчивается не позже своей длительности с небольшим запасом.
	//
	// Раньше здесь был один сон на всю длительность и одна проверка в конце.
	// Из-за этого приложение узнавало о происходящем последним: скипнул
	// стример заказ прямо в Spotify — панель показывала его ещё три минуты.
	// Теперь заглядываем регулярно, но выход по времени остался жёстким.
	length := time.Duration(item.DurationMs) * time.Millisecond
	if length <= 0 {
		length = 30 * time.Second
	}
	deadline := time.Now().Add(length + 15*time.Second)

	left := length
	// paused — сколько в сумме простояли на паузе. Своя мерка, отдельная от
	// общего срока: пауза не должна его растягивать без предела.
	var paused time.Duration
	// wasPaused — на прошлой проверке музыка стояла. Тогда сменившийся трек
	// означает «переключил человек», а не «доиграл сам».
	var wasPaused bool

	for ctx.Err() == nil {
		if time.Now().After(deadline) {
			p.log.Info("заказ доиграл по времени", "трек", item.Artist+" — "+item.Title)
			return true
		}

		// Шаг зависит от остатка: посреди трека заглядывать часто незачем,
		// а под конец — наоборот, иначе следующий заказ опоздает.
		nap := p.awaitNap(left)

		// Проснёмся ровно к концу трека? Тогда всё, что окажется в Spotify
		// после сна, — это уже не наш заказ, а то, чем Spotify продолжил сам.
		// Отличать это от «стример переключил руками» приходится здесь:
		// позже, из возврата, чужой трек выглядит одинаково в обоих случаях.
		toEnd := nap+2*time.Second >= left && !wasPaused

		if !p.napOrWake(ctx, nap) {
			p.log.Info("заказ скипнут", "трек", item.Title)
			return false
		}

		st, ok, err := p.spotify.State(ctx)
		if err != nil {
			// Spotify не ответил. Гадать не будем: досидим по своим часам.
			p.log.Warn("не проверил, доиграл ли трек", "ошибка", err)
			left = time.Until(deadline)
			continue
		}
		if !ok || st.Item == nil || st.Item.URI != item.URI {
			p.log.Info("заказ больше не играет в Spotify",
				"трек", item.Artist+" — "+item.Title, "доиграл_сам", toEnd)
			return toEnd
		}

		// Перемотка сбивает отсчёт: точка отсчёта переставляется на настоящее
		// положение, и панель с виджетом узнают об этом сразу.
		p.reposition(st.ProgressMs)

		left = time.Duration(st.Item.DurationMs-st.ProgressMs) * time.Millisecond

		// Трек на паузе у самого конца — это конец, а не пауза: Spotify так
		// показывает доигравший трек, и ждать тут нечего.
		if !st.IsPlaying && left <= 2*time.Second {
			return true
		}
		if left <= 0 {
			return true
		}
		if !st.IsPlaying {
			// Настоящая пауза: стример остановил музыку сам. Ждём его, но не
			// бесконечно.
			//
			// Раньше срок продлевался на десять секунд на каждой проверке, а
			// проверки шли чаще, — то есть убегал быстрее, чем шло время, и
			// заказ висел «играет» вечно. Теперь у паузы свой общий запас.
			paused += p.step()
			if paused > maxPause {
				// Музыку остановил человек — возврату здесь делать нечего.
				p.log.Info("заказ снят: Spotify стоит на паузе слишком долго",
					"трек", item.Artist+" — "+item.Title)
				return false
			}

			// Срок только продлеваем, никогда не сокращаем.
			//
			// Раньше здесь стояло `deadline = time.Now().Add(...)`, то есть
			// срок пересчитывался заново и переставал зависеть от остатка
			// трека. Десятиминутный заказ, поставленный на паузу на десятой
			// секунде, получал срок «шесть минут от сих» — и ровно на шестой
			// минуте обрывался посреди песни, а в историю писался как
			// «отыгравший». Стример при этом ничего не нажимал.
			if until := time.Now().Add(left + maxPause - paused); until.After(deadline) {
				deadline = until
			}

			// И отмечаем, что музыка стоит: если на следующей проверке в
			// Spotify окажется другой трек, это переключил человек, а не
			// «заказ доиграл». Раньше здесь ещё и подменялся остаток
			// (`left = p.step()`), из-за чего toEnd на паузе выходил всегда
			// истинным, и оборванный заказ выдавался за отыгравший — вместе
			// с возвратом музыки поверх того, что стример только что выбрал.
			wasPaused = true
			continue
		}
		wasPaused = false
	}
	return false
}

// maxPause — сколько всего готовы ждать стримера, если он поставил паузу.
// Больше пяти минут — это уже не пауза, а «отошёл»: очередь при этом стоять
// не должна.
const maxPause = 5 * time.Minute

// awaitNap — сколько спать до следующей проверки.
func (p *Player) awaitNap(left time.Duration) time.Duration {
	step := p.step()
	floor := step / 10
	if floor < 50*time.Millisecond {
		floor = 50 * time.Millisecond
	}
	switch {
	case left < floor:
		return floor
	case left > step:
		return step
	default:
		return left
	}
}

func (p *Player) step() time.Duration {
	if p.PollEvery > 0 {
		return p.PollEvery
	}
	return defaultPoll
}

// defaultPoll — как часто заглядываем в Spotify, пока играет заказ. Тот же
// шаг, что и у опроса своей музыки: за минуту десять запросов, лимит Spotify
// считает тысячами.
const defaultPoll = 6 * time.Second

// reposition переставляет точку отсчёта на настоящее положение в треке.
//
// Elapsed() считает от момента запуска, а трек можно перемотать — тогда весь
// дальнейший отсчёт врёт. Двигаем момент запуска так, чтобы Elapsed() снова
// показывал правду.
func (p *Player) reposition(progressMs int) {
	p.mu.Lock()
	if p.now == nil {
		p.mu.Unlock()
		return
	}
	want := time.Now().Add(-time.Duration(progressMs) * time.Millisecond)
	drift := want.Sub(p.now.StartedAt)
	if drift < 0 {
		drift = -drift
	}
	// Мелкие расхождения — обычная задержка сети, дёргать из-за них панель
	// незачем. Двигаем, только когда действительно перемотали.
	if drift < 2*time.Second {
		p.mu.Unlock()
		return
	}
	p.now.StartedAt = want
	p.mu.Unlock()

	p.log.Debug("положение в треке уточнено", "позиция_мс", progressMs)
	p.changed()
}

// restore возвращает Spotify туда, где он был до заказов.
func (p *Player) restore(ctx context.Context) {
	snap := p.Snapshot()
	if snap == nil {
		return
	}

	// Возврату надо сказать, что играло последним. Без этого он видит в
	// Spotify незнакомый трек — наш же заказ — и решает, что стример
	// переключил музыку сам, а значит трогать её нельзя. Итог: музыка не
	// возвращалась почти никогда, а в панели висело «переключили вручную».
	p.mu.Lock()
	played := p.lastPlayed
	// Заказ доиграл сам — значит стример за это время ни во что не вмешался,
	// и защита «музыку переключили вручную» здесь только мешает.
	//
	// Мешает она вот почему: заказ мы включаем одним треком, без источника, и
	// Spotify после него запускает автоподбор — какую-то похожую музыку. Для
	// возврата это чужой трек, неотличимый от того, что стример выбрал сам, —
	// и возврат отказывался работать, а плейлист не возвращался вообще
	// никогда. Оборванный заказ — другое дело: там чужой трек и правда может
	// оказаться выбором человека, и туда мы не лезем.
	force := p.endedNaturally
	p.mu.Unlock()

	// Возврат — это несколько запросов подряд с повторами. Своего срока у
	// него не было, и на упавшей сети он останавливал всю очередь.
	ctx, cancel := context.WithTimeout(ctx, restoreTimeout)
	defer cancel()

	outcome, err := p.spotify.Restore(ctx, snap, played, force)
	if err != nil {
		p.log.Warn("не вернул Spotify на место", "ошибка", err)
		p.fail(err)

		// Сорвалось ошибкой — снимок не отработал. Выбросить его сейчас
		// значит потерять плейлист стримера навсегда: следующий снимок
		// снимется уже с нашего же заказа. Держим и пробуем ещё.
		p.mu.Lock()
		p.restoreTries++
		again := p.restoreTries < maxRestoreTries
		p.mu.Unlock()
		if again {
			p.log.Info("возврат попробуем ещё раз", "попытка", p.restoreTries)
			return
		}
		p.log.Warn("возврат не вышел, снимок забыт", "попыток", p.restoreTries)
	}

	if p.OnRestored != nil {
		p.OnRestored(outcome, snap)
	}

	// Снимок отработал: держать его дальше нельзя, иначе следующая пачка
	// заказов вернёт музыку на вчерашнее место.
	p.mu.Lock()
	p.snap = nil
	p.restoreTries = 0
	p.endedNaturally = false
	p.mu.Unlock()
	p.changed()
}

// maxRestoreTries — сколько раз пробуем вернуть музыку, если возврат падает
// ошибкой. Три попытки с промежутком в полминуты переживают и перезапуск
// Spotify, и моргнувшую сеть; дальше держаться за снимок бессмысленно.
const maxRestoreTries = 3

// notOurOwn не даёт запомнить как «музыку стримера» наш же отыгравший заказ.
//
// Так бывает, когда прошлый возврат не сработал: заказ доиграл, в Spotify
// висит он же, приходит следующий заказ — и снимок снимается с нашего трека.
// Возвращаться по такому снимку некуда: плейлист стримера в нём уже потерян,
// а приложение уверено, что всё в порядке. Честнее сказать «возвращать
// нечего» — тогда после очереди включится запасной плейлист.
func (p *Player) notOurOwn(snap *spotify.Snapshot) *spotify.Snapshot {
	if snap == nil || snap.Empty || snap.TrackURI == "" {
		return snap
	}

	p.mu.Lock()
	ours := snap.TrackURI == p.lastPlayed
	p.mu.Unlock()
	if !ours {
		return snap
	}

	p.log.Warn("в Spotify всё ещё наш прошлый заказ — запоминать нечего",
		"трек", snap.ArtistName+" — "+snap.TrackName)
	return &spotify.Snapshot{
		CapturedAt: snap.CapturedAt,
		Empty:      true,
		DeviceID:   snap.DeviceID,
		DeviceName: snap.DeviceName,
	}
}

// restoreTimeout — сколько даём возврату. Внутри несколько запросов с
// повторами; без срока плеер застревал в возврате на минуты, и новые заказы
// всё это время не играли.
const restoreTimeout = 45 * time.Second

// dropped — заказ не удалось включить.
//
// Отмечать такой заказ отыгравшим нельзя: баллы списаны, зритель ничего не
// услышал, и по истории всё выглядит благополучно. Раньше при закрытом
// Spotify так молча прокручивалась вся очередь разом.
func (p *Player) dropped(item queue.Item, err error) {
	p.log.Warn("заказ не сыграл", "трек", item.Artist+" — "+item.Title, "ошибка", err)
	if p.OnDropped != nil {
		p.OnDropped(item, err)
	}
}

func (p *Player) finish(item queue.Item, natural bool) {
	if p.OnFinished != nil {
		p.OnFinished(item, natural)
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

// SetOptions меняет настройки работающего плеера.
func (p *Player) SetOptions(resumeDelay time.Duration, waitForCurrent bool) {
	p.mu.Lock()
	p.ResumeDelay = resumeDelay
	p.WaitForCurrent = waitForCurrent
	p.mu.Unlock()
}

// options отдаёт настройки под блокировкой.
func (p *Player) options() (resumeDelay time.Duration, waitForCurrent bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ResumeDelay, p.WaitForCurrent
}

// drainSkip выбрасывает залежавшийся сигнал скипа.
func (p *Player) drainSkip() {
	select {
	case <-p.skip:
	default:
	}
}
