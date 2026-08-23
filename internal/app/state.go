// Package app хранит состояние приложения, которое видит панель.
//
// Состояние меняется только из обработчиков событий (редемпшен, конец трека,
// команда модератора). Никаких фоновых опросов здесь нет: панель узнаёт об
// изменениях из WebSocket, а в простое приложение не делает вообще ничего.
package app

import (
	"sync"
	"time"
)

// ConnState — состояние одного внешнего подключения для лампочки в панели.
type ConnState struct {
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	// Detail — человеческая подсказка: «Не настроено», «Слетела авторизация».
	// Никаких «401 Unauthorized» тут быть не должно.
	Detail string `json:"detail"`
}

// NowPlaying — что играет прямо сейчас.
type NowPlaying struct {
	Provider   string `json:"provider"` // spotify | youtube | ""
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	CoverURL   string `json:"cover_url"`
	Requester  string `json:"requester"`
	PositionMs int    `json:"position_ms"`
	DurationMs int    `json:"duration_ms"`
	Uncertain  bool   `json:"uncertain"`
}

// QueueItem — заказ в очереди.
type QueueItem struct {
	ID         int64  `json:"id"`
	Source     string `json:"source"`
	Requester  string `json:"requester"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Provider   string `json:"provider"`
	DurationMs int    `json:"duration_ms"`
	Uncertain  bool   `json:"uncertain"`
}

// Notice — сообщение для стримера в панели.
//
// Code — короткий код вроде SP-04. Он нужен, чтобы стример мог назвать ошибку
// голосом, не пересказывая текст и не выгружая лог ради одной строчки.
type Notice struct {
	Level string    `json:"level"` // info | warn | error
	Code  string    `json:"code"`
	Text  string    `json:"text"`
	At    time.Time `json:"at"`
}

// SpotifyInfo — состояние Spotify для панели.
type SpotifyInfo struct {
	// HasClientID отличает «ещё не настроено» от «настроено, но отвалилось».
	// Для первого запуска это разные экраны: в одном случае человеку надо
	// вставить Client ID, в другом — нажать одну кнопку.
	HasClientID bool   `json:"has_client_id"`
	Connected   bool   `json:"connected"`
	Account     string `json:"account"`
	Email       string `json:"email"`
	// Plan: premium | free | unknown. Три состояния, а не флаг: «не смогли
	// определить» — это не то же самое, что «подписки нет».
	Plan         string     `json:"plan"`
	PlanLabel    string     `json:"plan_label"`
	PlanNote     string     `json:"plan_note"` // что делать, если что-то не так
	PlanNoteCode string     `json:"plan_note_code"`
	SnapshotText string     `json:"snapshot_text"`
	SnapshotAt   *time.Time `json:"snapshot_at"`
}

// TwitchInfo — состояние Twitch для панели.
type TwitchInfo struct {
	HasClientID bool   `json:"has_client_id"`
	Connected   bool   `json:"connected"`
	Channel     string `json:"channel"`
	ChannelType string `json:"channel_type"` // партнёр | аффилиат | обычный
	HasPoints   bool   `json:"has_points"`

	RewardTitle string `json:"reward_title"`
	RewardCost  int    `json:"reward_cost"`
	RewardReady bool   `json:"reward_ready"`

	// Код для входа: стример открывает адрес на любом устройстве и вводит код.
	PendingCode    string     `json:"pending_code"`
	PendingURL     string     `json:"pending_url"`
	PendingExpires *time.Time `json:"pending_expires"`

	Note     string `json:"note"`
	NoteCode string `json:"note_code"`
}

// RedemptionView — заказ за баллы. На этом этапе очереди ещё нет, поэтому
// заказы просто копятся списком, чтобы можно было проверить приём и возврат.
type RedemptionView struct {
	ID       string    `json:"id"`
	RewardID string    `json:"reward_id"`
	User     string    `json:"user"`
	Text     string    `json:"text"`
	Cost     int       `json:"cost"`
	At       time.Time `json:"at"`
	Status   string    `json:"status"` // новый | баллы возвращены | выполнен
}

// Snapshot — вся картинка целиком, ровно то, что уходит в панель одним JSON.
type Snapshot struct {
	Connections []ConnState      `json:"connections"`
	Now         *NowPlaying      `json:"now"`
	Queue       []QueueItem      `json:"queue"`
	Notices     []Notice         `json:"notices"`
	Paused      bool             `json:"paused"` // приём заказов остановлен
	Version     string           `json:"version"`
	Spotify     SpotifyInfo      `json:"spotify"`
	Twitch      TwitchInfo       `json:"twitch"`
	Redemptions []RedemptionView `json:"redemptions"`
	DebugLog    bool             `json:"debug_log"`
}

// State — потокобезопасное состояние с уведомлением подписчиков об изменениях.
type State struct {
	mu          sync.RWMutex
	conns       map[string]ConnState
	order       []string // порядок лампочек в панели, фиксированный
	now         *NowPlaying
	queue       []QueueItem
	notices     []Notice
	paused      bool
	version     string
	spotify     SpotifyInfo
	twitch      TwitchInfo
	redemptions []RedemptionView
	debug       bool

	subs map[int]chan struct{}
	next int
}

const maxNotices = 50

// maxRedemptions — сколько заказов держим в списке до появления очереди.
const maxRedemptions = 20

// New создаёт состояние с погашенными лампочками подключений.
func New(version string) *State {
	s := &State{
		conns:   map[string]ConnState{},
		subs:    map[int]chan struct{}{},
		version: version,
	}
	for _, name := range []string{"Spotify", "Twitch", "DonationAlerts", "DonatePay"} {
		s.order = append(s.order, name)
		s.conns[name] = ConnState{Name: name, Connected: false, Detail: "Не настроено"}
	}
	return s
}

// Subscribe отдаёт канал-звонок: в него приходит сигнал при любом изменении.
// Канал буферизован на 1 — если подписчик отстал, лишние сигналы отбрасываются,
// он всё равно прочитает актуальный снимок целиком.
func (s *State) Subscribe() (<-chan struct{}, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.next
	s.next++
	ch := make(chan struct{}, 1)
	s.subs[id] = ch

	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if c, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(c)
		}
	}
}

// notify вызывается под уже взятой блокировкой записи.
func (s *State) notify() {
	for _, ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default: // подписчик ещё не забрал прошлый сигнал — и не надо
		}
	}
}

// SetConn обновляет лампочку подключения.
func (s *State) SetConn(name string, connected bool, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conns[name] = ConnState{Name: name, Connected: connected, Detail: detail}
	s.notify()
}

// SetNow задаёт текущий трек (nil — тишина).
func (s *State) SetNow(n *NowPlaying) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = n
	s.notify()
}

// SetQueue заменяет очередь целиком.
func (s *State) SetQueue(q []QueueItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = q
	s.notify()
}

// SetPaused включает или выключает приём заказов.
func (s *State) SetPaused(p bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = p
	s.notify()
}

// SetSpotify обновляет карточку Spotify в панели.
func (s *State) SetSpotify(info SpotifyInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spotify = info
	s.notify()
}

// UpdateSpotify меняет часть сведений о Spotify, не трогая остальные.
func (s *State) UpdateSpotify(fn func(*SpotifyInfo)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.spotify)
	s.notify()
}

// SetTwitch обновляет карточку Twitch в панели.
func (s *State) SetTwitch(info TwitchInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.twitch = info
	s.notify()
}

// UpdateTwitch меняет часть сведений о Twitch, не трогая остальные.
func (s *State) UpdateTwitch(fn func(*TwitchInfo)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.twitch)
	s.notify()
}

// AddRedemption кладёт новый заказ в начало списка.
func (s *State) AddRedemption(r RedemptionView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.redemptions = append([]RedemptionView{r}, s.redemptions...)
	if len(s.redemptions) > maxRedemptions {
		s.redemptions = s.redemptions[:maxRedemptions]
	}
	s.notify()
}

// SetRedemptionStatus помечает заказ как возвращённый или выполненный.
func (s *State) SetRedemptionStatus(id, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.redemptions {
		if s.redemptions[i].ID == id {
			s.redemptions[i].Status = status
			break
		}
	}
	s.notify()
}

// SetDebugLog запоминает, включён ли подробный лог (галочка в панели).
func (s *State) SetDebugLog(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.debug = on
	s.notify()
}

// Notify добавляет сообщение для стримера.
func (s *State) Notify(level, text string) {
	s.NotifyCode(level, "", text)
}

// NotifyCode добавляет сообщение с кодом ошибки.
func (s *State) NotifyCode(level, code, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notices = append(s.notices, Notice{Level: level, Code: code, Text: text, At: time.Now()})
	if len(s.notices) > maxNotices {
		s.notices = s.notices[len(s.notices)-maxNotices:]
	}
	s.notify()
}

// Snapshot собирает копию состояния для отправки в панель.
func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Пустые срезы, а не nil: панель ждёт массив и на null споткнётся.
	snap := Snapshot{
		Queue:       append(make([]QueueItem, 0, len(s.queue)), s.queue...),
		Notices:     append(make([]Notice, 0, len(s.notices)), s.notices...),
		Paused:      s.paused,
		Version:     s.version,
		Spotify:     s.spotify,
		Twitch:      s.twitch,
		Redemptions: append(make([]RedemptionView, 0, len(s.redemptions)), s.redemptions...),
		DebugLog:    s.debug,
	}
	for _, name := range s.order {
		snap.Connections = append(snap.Connections, s.conns[name])
	}
	if s.now != nil {
		now := *s.now
		snap.Now = &now
	}
	return snap
}
