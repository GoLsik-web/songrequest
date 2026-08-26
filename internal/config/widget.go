package config

import "strings"

// Настройки виджета.
//
// Раньше оформление задавалось параметрами в адресе, и каждое изменение
// означало «скопируй новый адрес и вставь его в OBS заново». Теперь адрес
// один и навсегда, а всё оформление живёт здесь: стример правит его в панели,
// виджет подхватывает на лету, OBS трогать не нужно.
type Widget struct {
	// Preset — выбранный набор, «семейство/вариант»: efir/lime, vinyl/sepia.
	// Семейство задаёт устройство плашки, вариант — цвета и настроение.
	Preset string `json:"preset"`

	Position string  `json:"position"` // left-bottom, right-top и так далее
	Scale    float64 `json:"scale"`    // 0.5…2
	Gap      int     `json:"gap"`      // отступ от края кадра, px

	// Что показывать. Обложка занимает место, ник заказчика нужен не всем,
	// полоса времени кому-то мешает — пусть решает стример.
	ShowArt       bool `json:"show_art"`
	ShowRequester bool `json:"show_requester"`
	ShowBar       bool `json:"show_bar"`

	// ShowOwn — показывать ли музыку, которую стример поставил сам, а не
	// заказали зрители. По умолчанию да: чаще всего в кадре хотят видеть
	// «что играет», а не «что заказали». Кому свой плейлист светить не
	// хочется — выключает.
	ShowOwn bool `json:"show_own"`

	// Motion — движение. Анимация у каждого набора своя и по смыслу самого
	// предмета, но кому-то в кадре нужен полный покой.
	Motion bool `json:"motion"`

	// Сколько секунд плашка висит после начала трека. 0 — весь трек.
	// Некоторым нужен короткий показ в начале, а не постоянная плашка.
	HoldSeconds int `json:"hold_seconds"`

	// Tweaks — ручная правка поверх набора: значения токенов оформления.
	// Пустая карта означает «как в наборе». Ключи проверяются при записи,
	// чтобы в стиль страницы не попало что попало.
	Tweaks map[string]string `json:"tweaks,omitempty"`
}

// DefaultWidget — то, с чего начинают все.
func DefaultWidget() Widget {
	return Widget{
		Preset:        "efir/lime",
		Position:      "left-bottom",
		Scale:         1,
		Gap:           40,
		ShowArt:       true,
		ShowRequester: true,
		ShowBar:       true,
		Motion:        true,
		ShowOwn:       true,
	}
}

// tweakKeys — что стример может править руками. Список закрытый: значения
// уходят в style страницы, и принимать произвольные имена оттуда нельзя.
var tweakKeys = map[string]bool{
	"--w-bg": true, "--w-ink": true, "--w-sub": true, "--w-accent": true,
	"--w-line": true, "--w-radius": true, "--w-pad": true, "--w-blur": true,
	"--w-edge": true, "--w-art": true, "--w-art-radius": true,
	"--w-title-size": true, "--w-shadow": true, "--w-title-font": true,
	"--w-body-font": true, "--w-caps": true, "--w-weight": true,
}

// Normalize приводит настройки к рабочему виду: чинит пустое и выкидывает
// то, чего мы не понимаем. Виджет висит на стриме — он не должен ломаться
// из-за руками поправленного config.json.
func (w *Widget) Normalize() {
	if strings.TrimSpace(w.Preset) == "" {
		w.Preset = "efir/lime"
	}
	switch w.Position {
	case "left-bottom", "right-bottom", "left-top", "right-top":
	default:
		w.Position = "left-bottom"
	}
	if w.Scale < 0.5 || w.Scale > 2 {
		w.Scale = 1
	}
	if w.Gap < 0 || w.Gap > 400 {
		w.Gap = 40
	}
	if w.HoldSeconds < 0 || w.HoldSeconds > 3600 {
		w.HoldSeconds = 0
	}

	for key, value := range w.Tweaks {
		// Значение уходит прямо в style, поэтому проверяем строго.
		//
		// Точка с запятой и фигурные скобки позволили бы дописать в стиль
		// что угодно. Кавычки — вырваться из атрибута, если значение
		// когда-нибудь подставят строкой, а не через setProperty. А круглая
		// скобка закрывает url(...): строка вида «url(http://чужой/x.png)»
		// заставила бы OBS ходить на чужой сервер при каждой перерисовке
		// виджета — то есть посторонний человек узнавал бы IP стримера и
		// время, когда тот в эфире. Здесь и так бывают только цвета и
		// пиксели, а цвет через rgba() задаётся из наборов, не отсюда.
		if !tweakKeys[key] || strings.ContainsAny(value, ";{}<>\"'()") || len(value) > 64 {
			delete(w.Tweaks, key)
		}
	}
	if len(w.Tweaks) == 0 {
		w.Tweaks = nil
	}
}
