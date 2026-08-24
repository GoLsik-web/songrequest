// Package donations принимает заказы за донаты.
//
// Сервисов много и они разные, поэтому здесь только общий интерфейс: добавить
// новый — значит написать один файл, а не править очередь и панель.
package donations

import (
	"context"
	"sync"
	"time"

	"songrequest/internal/logx"
)

// Donation — донат, из которого может получиться заказ.
type Donation struct {
	// ID — идентификатор у сервиса. По нему отсекаем повторы: сервисы
	// переприсылают события при переподключении, и без этого один донат
	// превратился бы в три заказа.
	ID string
	// Source — какой сервис: donationalerts, donatepay.
	Source   string
	Username string
	Message  string
	Amount   float64
	Currency string
	At       time.Time
}

// Source — сервис донатов. Добавить новый = реализовать этот интерфейс.
type Source interface {
	// Name — как сервис называется в панели.
	Name() string
	// Configured сообщает, настроен ли сервис. Ненастроенный не показывается
	// как сломанный: это не поломка, а «мы им не пользуемся».
	Configured() bool
	// Run держит подключение до отмены контекста и зовёт onDonation.
	Run(ctx context.Context, onDonation func(Donation))
}

// StatusFn сообщает панели о состоянии подключения.
type StatusFn func(name string, connected bool, detail string)

// Hub держит все источники донатов разом.
type Hub struct {
	log     *logx.Logger
	sources []Source

	// OnDonation вызывается на каждый новый донат.
	OnDonation func(Donation)
	// OnStatus сообщает панели, жив ли сервис.
	OnStatus StatusFn

	mu   sync.Mutex
	seen map[string]time.Time
}

// NewHub создаёт хаб.
func NewHub(log *logx.Logger) *Hub {
	return &Hub{log: log, seen: map[string]time.Time{}}
}

// Add подключает источник.
func (h *Hub) Add(s Source) { h.sources = append(h.sources, s) }

// Run поднимает все настроенные источники.
func (h *Hub) Run(ctx context.Context) {
	for _, src := range h.sources {
		if !src.Configured() {
			h.status(src.Name(), false, "Не настроено")
			continue
		}
		go src.Run(ctx, h.handle)
	}
}

// Handle — публичная точка входа: источник, поднятый уже после старта,
// отдаёт донаты сюда же и проходит ту же проверку на повторы.
func (h *Hub) Handle(d Donation) { h.handle(d) }

// handle отсеивает повторы и передаёт донат дальше.
func (h *Hub) handle(d Donation) {
	if h.duplicate(d) {
		h.log.Debug("повторный донат пропущен", "сервис", d.Source, "id", d.ID)
		return
	}

	h.log.Info("донат",
		"сервис", d.Source, "от", d.Username,
		"сумма", d.Amount, "валюта", d.Currency, "сообщение", d.Message)

	if h.OnDonation != nil {
		h.OnDonation(d)
	}
}

// duplicate помнит недавние донаты. Сервисы переприсылают события при
// переподключении, и один донат не должен стать тремя заказами.
func (h *Hub) duplicate(d Donation) bool {
	if d.ID == "" {
		return false
	}
	key := d.Source + ":" + d.ID

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.seen[key]; ok {
		return true
	}
	h.seen[key] = time.Now()

	// Чистим старое, чтобы карта не росла весь стрим.
	for k, at := range h.seen {
		if time.Since(at) > time.Hour {
			delete(h.seen, k)
		}
	}
	return false
}

func (h *Hub) status(name string, connected bool, detail string) {
	if h.OnStatus != nil {
		h.OnStatus(name, connected, detail)
	}
}

// Status отдаёт функцию сообщения о состоянии для источника.
func (h *Hub) Status(name string) func(bool, string) {
	return func(connected bool, detail string) { h.status(name, connected, detail) }
}
