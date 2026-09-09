package spotify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"songrequest/internal/errs"
)

// PlayerState — то, что Spotify рассказывает о текущем воспроизведении.
type PlayerState struct {
	Device struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Type         string `json:"type"`
		IsActive     bool   `json:"is_active"`
		IsRestricted bool   `json:"is_restricted"`
		Volume       int    `json:"volume_percent"`
	} `json:"device"`
	RepeatState  string `json:"repeat_state"`
	ShuffleState bool   `json:"shuffle_state"`
	Context      *struct {
		Type string `json:"type"`
		URI  string `json:"uri"`
	} `json:"context"`
	ProgressMs int  `json:"progress_ms"`
	IsPlaying  bool `json:"is_playing"`
	Item       *struct {
		URI        string `json:"uri"`
		ID         string `json:"id"`
		Name       string `json:"name"`
		DurationMs int    `json:"duration_ms"`
		Artists    []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"artists"`
		Album struct {
			Name   string `json:"name"`
			Images []struct {
				URL string `json:"url"`
			} `json:"images"`
		} `json:"album"`
	} `json:"item"`
	CurrentlyPlayingType string `json:"currently_playing_type"`
}

// Device — устройство Spotify Connect.
type Device struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	IsActive     bool   `json:"is_active"`
	IsRestricted bool   `json:"is_restricted"`
}

// Snapshot — слепок того, что играло до вмешательства заказом.
// Это главная структура всего приложения: по ней мы возвращаем стримеру
// ровно то состояние, в котором его прервали.
type Snapshot struct {
	CapturedAt  time.Time `json:"captured_at"`
	Empty       bool      `json:"empty"` // плеер молчал, возвращать нечего
	DeviceID    string    `json:"device_id"`
	DeviceName  string    `json:"device_name"`
	ContextURI  string    `json:"context_uri"`
	ContextType string    `json:"context_type"`
	TrackURI    string    `json:"track_uri"`
	TrackName   string    `json:"track_name"`
	ArtistName  string    `json:"artist_name"`
	ArtistID    string    `json:"artist_id"`
	PositionMs  int       `json:"position_ms"`
	DurationMs  int       `json:"duration_ms"`
	IsPlaying   bool      `json:"is_playing"`
	Shuffle     bool      `json:"shuffle"`
	Repeat      string    `json:"repeat"`
	// Volume — громкость устройства в процентах на момент снимка. Нужна
	// заказам с YouTube: их играет отдельная программа, и без этого числа
	// она включается на полную, оглушая эфир.
	Volume int `json:"volume"`
}

// Describe — короткое описание снимка для панели.
func (s *Snapshot) Describe() string {
	if s == nil {
		return "снимок не снят"
	}
	if s.Empty {
		return "в момент снимка ничего не играло"
	}
	where := s.DeviceName
	if where == "" {
		where = "устройство неизвестно"
	}
	return fmt.Sprintf("%s — %s · %s · %s · %s",
		s.ArtistName, s.TrackName, mmss(s.PositionMs), s.ContextLabel(), where)
}

// ContextLabel объясняет человеческими словами, откуда играла музыка и чем
// это грозит при возврате. Стример видит это в панели ещё до заказа.
func (s *Snapshot) ContextLabel() string {
	if s == nil || s.Empty {
		return "ничего не играло"
	}
	switch {
	case s.ContextURI == "":
		return "без источника — вернём только трек"
	case strings.HasPrefix(s.ContextURI, "spotify:playlist:"):
		return "плейлист — вернётся полностью"
	case strings.HasPrefix(s.ContextURI, "spotify:album:"):
		return "альбом — вернётся полностью"
	case strings.HasPrefix(s.ContextURI, "spotify:artist:"):
		return "радио артиста — вернём только трек"
	case strings.HasPrefix(s.ContextURI, "spotify:collection"):
		return "любимые треки — вернём только трек"
	default:
		return s.ContextType + " — вернём только трек"
	}
}

func mmss(ms int) string {
	sec := ms / 1000
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

// State читает текущее состояние плеера. Второе значение false означает,
// что Spotify молчит: плеер закрыт или ничего не играет.
func (c *Client) State(ctx context.Context) (*PlayerState, bool, error) {
	var st PlayerState
	err := c.do(ctx, http.MethodGet, "/me/player", nil, &st)
	if errors.Is(err, errNoContent) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &st, true, nil
}

// Devices перечисляет устройства Spotify Connect.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	var out struct {
		Devices []Device `json:"devices"`
	}
	if err := c.do(ctx, http.MethodGet, "/me/player/devices", nil, &out); err != nil {
		if errors.Is(err, errNoContent) {
			return nil, nil
		}
		return nil, err
	}
	return out.Devices, nil
}

// Capture снимает состояние перед тем, как вклиниться с заказом.
func (c *Client) Capture(ctx context.Context) (*Snapshot, error) {
	st, playing, err := c.State(ctx)
	if err != nil {
		return nil, errs.Wrap(errs.SpotifySnapshot, "Не смог запомнить, что играло в Spotify.", err)
	}

	if !playing || st.Item == nil {
		// Тишина — тоже валидный снимок: он говорит «возвращать нечего»,
		// и после заказа мы честно ничего не включим.
		snap := &Snapshot{CapturedAt: time.Now(), Empty: true}
		c.log.Info("снимок Spotify: ничего не играло")
		return snap, nil
	}

	snap := &Snapshot{
		CapturedAt: time.Now(),
		DeviceID:   st.Device.ID,
		DeviceName: st.Device.Name,
		TrackURI:   st.Item.URI,
		TrackName:  st.Item.Name,
		PositionMs: st.ProgressMs,
		DurationMs: st.Item.DurationMs,
		IsPlaying:  st.IsPlaying,
		Shuffle:    st.ShuffleState,
		Repeat:     st.RepeatState,
		Volume:     st.Device.Volume,
	}
	if len(st.Item.Artists) > 0 {
		snap.ArtistName = st.Item.Artists[0].Name
		snap.ArtistID = st.Item.Artists[0].ID
	}
	if st.Context != nil {
		snap.ContextURI = st.Context.URI
		snap.ContextType = st.Context.Type
	}

	c.log.Info("снял снимок Spotify",
		"трек", snap.TrackName, "артист", snap.ArtistName,
		"контекст", snap.ContextURI, "позиция_мс", snap.PositionMs,
		"играл", snap.IsPlaying, "устройство", snap.DeviceName,
		"громкость", snap.Volume)
	return snap, nil
}

// RestoreOutcome — чем закончилась попытка вернуть всё как было.
type RestoreOutcome struct {
	Restored bool      `json:"restored"`
	Code     errs.Code `json:"code"`
	Message  string    `json:"message"`
	// ContextLost — трек вернули, а источник (плейлист, радио) нет. После
	// такого трека Spotify замолчит, поэтому продолжение надо дособрать самим.
	ContextLost bool   `json:"context_lost"`
	DeviceID    string `json:"device_id"`
}

// Restore возвращает Spotify в состояние из снимка.
//
// playedURI — трек, который мы играли своим заказом. Он нужен, чтобы понять,
// не переключил ли стример музыку руками: если сейчас играет что-то третье,
// снимок протух и лезть в чужое воспроизведение нельзя.
//
// force снимает эту защиту. Она нужна, когда приложение возвращает музыку
// само, после очереди. Но если стример нажал «Вернуть как было» руками, он
// уже сказал, чего хочет, — спорить с ним не о чем.
func (c *Client) Restore(ctx context.Context, snap *Snapshot, playedURI string, force bool) (RestoreOutcome, error) {
	if snap == nil || snap.Empty {
		return RestoreOutcome{
			Code:    errs.SpotifyNothing,
			Message: "Возвращать нечего: в момент заказа Spotify ничего не играл.",
		}, nil
	}

	current, playing, err := c.State(ctx)
	if err != nil {
		c.log.Warn("не прочитал состояние перед возвратом", "ошибка", err)
	}

	if !force && playing && current != nil && current.Item != nil && IsStale(snap, current.Item.URI, playedURI) {
		c.log.Info("снимок протух: музыку переключили руками",
			"сейчас_играет", current.Item.Name, "ожидали", playedURI)
		return RestoreOutcome{
			Code:    errs.SpotifyStale,
			Message: "Музыку переключили вручную, поэтому ничего не трогаю.",
		}, nil
	}

	devices, err := c.Devices(ctx)
	if err != nil {
		return RestoreOutcome{}, err
	}

	deviceID, derr := ChooseDevice(snap, devices)
	if derr != nil {
		return RestoreOutcome{}, derr
	}

	// Устройство могло уснуть, пока играл заказ. Тогда переносим на него
	// воспроизведение — иначе Spotify ответит «нет активного устройства».
	if needTransfer(devices, deviceID) {
		c.log.Info("переношу воспроизведение на устройство", "устройство", deviceID)
		if err := c.Transfer(ctx, deviceID, false); err != nil {
			return RestoreOutcome{}, errs.Wrap(errs.SpotifyRestore,
				"Не смог разбудить устройство Spotify.", err)
		}
		// Устройству нужно мгновение, чтобы объявиться активным.
		if !sleepCtx(ctx, 700*time.Millisecond) {
			return RestoreOutcome{}, ctx.Err()
		}
	}

	// Порядок важен: шаффл и повтор ставим до старта. Если включить их после,
	// Spotify успеет уехать на случайный следующий трек.
	if err := c.SetShuffle(ctx, snap.Shuffle, deviceID); err != nil {
		c.log.Warn("не восстановил шаффл", "ошибка", err)
	}
	if snap.Repeat != "" {
		if err := c.SetRepeat(ctx, snap.Repeat, deviceID); err != nil {
			c.log.Warn("не восстановил повтор", "ошибка", err)
		}
	}

	body, lostContext := BuildPlayBody(snap)
	if lostContext {
		c.log.Info("контекст не поддерживает возврат к треку, включаю трек отдельно",
			"тип_контекста", snap.ContextType)
	}

	if err := c.do(ctx, http.MethodPut, "/me/player/play?device_id="+url.QueryEscape(deviceID), body, nil); err != nil {
		return RestoreOutcome{}, errs.Wrap(errs.SpotifyRestore,
			"Не получилось вернуть музыку туда, где она была.", err)
	}

	// Дальше музыка уже играет, и что бы ни случилось ниже, возврат состоялся.
	//
	// Раньше здесь на истёкшем сроке возвращалось `RestoreOutcome{}, ctx.Err()`
	// — то есть удавшийся возврат отдавался провалом. Плеер в ответ держал
	// снимок и через полминуты возвращал музыку второй раз, поверх того, что
	// стример к тому времени слушал сам.

	// Стример слушал на паузе — вернём и паузу тоже. Перемотать трек, не
	// запустив его, Spotify не даёт, поэтому пауза ставится сразу после старта.
	if !snap.IsPlaying {
		if sleepCtx(ctx, 300*time.Millisecond) {
			if err := c.Pause(ctx, deviceID); err != nil {
				c.log.Warn("не вернул паузу", "ошибка", err)
			}
		} else {
			c.log.Warn("не успел вернуть паузу: вышел срок")
		}
	}

	c.log.Info("вернул Spotify в исходное состояние",
		"трек", snap.TrackName, "позиция_мс", snap.PositionMs, "контекст", snap.ContextURI)

	// Проверяем, что вышло на самом деле. Без этого приложение отчитывается
	// об успехе по факту отправки запроса, а стример видит другое — и спорить
	// с ним нечем.
	// Проверяем не для лога, а для отчёта: «вернул» без проверки — это отчёт
	// об отправленном запросе, а не о результате. Раньше несовпадение видел
	// только лог, а панель в любом случае писала бодрое «Вернул: …», и
	// дозаполнение тишины не срабатывало.
	if !c.verifyRestore(ctx, snap) {
		lostContext = true
	}

	msg := fmt.Sprintf("Вернул: %s — %s с %s.", snap.ArtistName, snap.TrackName, mmss(snap.PositionMs))
	if lostContext {
		msg += " Плейлист вернуть не вышло — Spotify не запоминает, откуда играла эта музыка."
	}
	return RestoreOutcome{
		Restored:    true,
		Message:     msg,
		ContextLost: lostContext,
		DeviceID:    deviceID,
	}, nil
}

// verifyRestore смотрит, что Spotify реально играет после возврата, и пишет
// это в лог. Отчёт «вернул» без проверки — это отчёт об отправленном запросе,
// а не о результате: разбирать по такому логу чужую проблему невозможно.
// Возвращает true, если Spotify действительно играет то, что мы просили.
// Проверить не удалось — считаем, что всё в порядке: спорить с человеком на
// основании неполученного ответа хуже, чем промолчать.
func (c *Client) verifyRestore(ctx context.Context, snap *Snapshot) bool {
	if !sleepCtx(ctx, time.Second) {
		return true
	}

	st, playing, err := c.State(ctx)
	if err != nil || !playing || st.Item == nil {
		c.log.Warn("после возврата плеер молчит", "ошибка", err)
		return true
	}

	gotContext := ""
	if st.Context != nil {
		gotContext = st.Context.URI
	}

	fields := []any{
		"ждали_трек", snap.TrackName, "получили_трек", st.Item.Name,
		"ждали_контекст", snap.ContextURI, "получили_контекст", gotContext,
		"ждали_позицию_мс", snap.PositionMs, "получили_позицию_мс", st.ProgressMs,
	}

	switch {
	case st.Item.URI != snap.TrackURI:
		c.log.Warn("после возврата играет не тот трек", fields...)
		return false
	case snap.ContextURI != "" && gotContext != snap.ContextURI:
		c.log.Warn("трек вернулся, а источник нет", fields...)
		return false
	default:
		c.log.Info("возврат подтверждён", fields...)
		return true
	}
}

// IsStale определяет, что стример сам переключил музыку, пока играл заказ.
//
// Правило простое: если сейчас звучит не наш заказ и не тот трек, к которому
// мы собирались вернуться, значит человек взял управление на себя.
func IsStale(snap *Snapshot, currentURI, playedURI string) bool {
	if snap == nil || currentURI == "" {
		return false
	}
	if playedURI != "" && currentURI == playedURI {
		return false // всё ещё играет наш заказ, всё по плану
	}
	// Уже вернулись сами (или заказ доиграл и Spotify продолжил с того же места).
	return currentURI != snap.TrackURI
}

// ChooseDevice выбирает, где возобновлять воспроизведение.
func ChooseDevice(snap *Snapshot, devices []Device) (string, error) {
	prefer := ""
	if snap != nil {
		prefer = snap.DeviceID
	}
	return chooseDeviceID(prefer, devices)
}

// chooseDeviceID — та же выборка, но без снимка: заказу тоже надо где-то
// играть, а снимка у него может не быть вовсе.
func chooseDeviceID(prefer string, devices []Device) (string, error) {
	if len(devices) == 0 {
		return "", errs.New(errs.SpotifyNoDevice,
			"Spotify нигде не запущен. Открой приложение Spotify на компьютере и включи любой трек.")
	}

	// Идеально — то же устройство, где играли до заказа.
	for _, d := range devices {
		if prefer != "" && d.ID == prefer && !d.IsRestricted {
			return d.ID, nil
		}
	}
	// Иначе то, что активно сейчас.
	for _, d := range devices {
		if d.IsActive && !d.IsRestricted {
			return d.ID, nil
		}
	}
	// Иначе любое, которым нам разрешено управлять.
	for _, d := range devices {
		if !d.IsRestricted {
			return d.ID, nil
		}
	}
	return "", errs.New(errs.SpotifyNoDevice,
		"Все устройства Spotify запрещают управление со стороны. Открой Spotify на компьютере и включи любой трек.")
}

// needTransfer сообщает, нужно ли переносить воспроизведение на устройство.
func needTransfer(devices []Device, deviceID string) bool {
	for _, d := range devices {
		if d.ID == deviceID {
			return !d.IsActive
		}
	}
	return true
}

// playBody — тело запроса на запуск воспроизведения.
type playBody struct {
	ContextURI string   `json:"context_uri,omitempty"`
	URIs       []string `json:"uris,omitempty"`
	Offset     *struct {
		URI string `json:"uri"`
	} `json:"offset,omitempty"`
	PositionMs int `json:"position_ms"`
}

// BuildPlayBody собирает запрос на возврат к нужному месту.
//
// Второе значение — признак того, что источник восстановить не вышло и играть
// будет один трек. Spotify принимает «начать с этого трека» только для альбома
// и плейлиста; во всех остальных случаях — радио артиста, любимые треки,
// автоподбор, вообще без источника — остаётся включить сам трек, и после него
// наступит тишина. Поэтому здесь true, а не только когда источник был известен:
// молчание после одного трека одинаково неприятно в любом из этих случаев.
func BuildPlayBody(snap *Snapshot) (any, bool) {
	if snap.ContextURI != "" && supportsOffset(snap.ContextURI) {
		body := &playBody{ContextURI: snap.ContextURI, PositionMs: snap.PositionMs}
		body.Offset = &struct {
			URI string `json:"uri"`
		}{URI: snap.TrackURI}
		return body, false
	}

	body := &playBody{PositionMs: snap.PositionMs}
	if snap.TrackURI != "" {
		body.URIs = []string{snap.TrackURI}
	}
	return body, true
}

func supportsOffset(contextURI string) bool {
	return strings.HasPrefix(contextURI, "spotify:album:") ||
		strings.HasPrefix(contextURI, "spotify:playlist:")
}

// PlayTrack включает один трек вне всякого контекста — так играются заказы.
//
// Уснувшее устройство здесь приходится будить самим, и вот почему. Заказ мы
// включаем списком из одного трека, без источника. Когда такой трек доигрывает,
// Spotify не продолжает ничего — воспроизведение просто останавливается, а
// программа Spotify через несколько секунд перестаёт быть «активным
// устройством». Для веб-API это значит «играть негде», и следующий заказ
// получал 404 NO_ACTIVE_DEVICE: с плейлиста на заказ переключалось нормально
// (там музыка играла, устройство было живым), а с заказа на заказ — уже нет.
// Со стороны стримера это выглядело как «второй трек просто не включается»,
// а зрителю ещё и возвращались баллы.
//
// Возврат музыки эту же беду разбирал давно (см. Restore), только заказы шли
// мимо: они звали play без устройства и без второй попытки.
func (c *Client) PlayTrack(ctx context.Context, trackURI, deviceID string) error {
	return c.play(ctx, &playBody{URIs: []string{trackURI}}, deviceID)
}

// play отправляет запрос на запуск и, если играть оказалось негде, будит
// устройство и повторяет.
//
// Общий для всех запусков нарочно: раньше побудка была только у заказов, и
// та же самая беда вылезала на шаг позже — когда после очереди включался
// запасной плейлист. Устройство к тому моменту уснуло точно так же, и
// стример получал «SP-06 · Spotify нигде не открыт» с тишиной в эфире.
func (c *Client) play(ctx context.Context, body *playBody, deviceID string) error {
	err := c.do(ctx, http.MethodPut, "/me/player/play"+deviceQuery(deviceID), body, nil)
	if err == nil || errs.CodeOf(err) != errs.SpotifyNoDevice {
		return err
	}

	id, wakeErr := c.wakeDevice(ctx, deviceID)
	if wakeErr != nil {
		// Разбудить нечего — значит Spotify и правда закрыт. Отдаём первую
		// ошибку: её текст написан для стримера и объясняет, что делать.
		c.log.Warn("некого будить перед запуском музыки", "ошибка", wakeErr)
		return err
	}

	c.log.Info("устройство уснуло, разбудил", "устройство", id)
	return c.do(ctx, http.MethodPut, "/me/player/play?device_id="+url.QueryEscape(id), body, nil)
}

// wakeDevice поднимает уснувшее устройство и отдаёт его id.
//
// prefer — то, на котором играли раньше: возвращаться на чужую колонку,
// когда стример слушает на компьютере, нельзя.
func (c *Client) wakeDevice(ctx context.Context, prefer string) (string, error) {
	devices, err := c.Devices(ctx)
	if err != nil {
		return "", err
	}
	id, err := chooseDeviceID(prefer, devices)
	if err != nil {
		return "", err
	}
	if !needTransfer(devices, id) {
		// Устройство активно, а play всё равно отказал: будить нечего, пусть
		// вторая попытка просто уйдёт с явным device_id.
		return id, nil
	}
	if err := c.Transfer(ctx, id, false); err != nil {
		return "", err
	}
	// Устройству нужно мгновение, чтобы объявиться активным. Столько же ждёт
	// возврат музыки — там это проверено.
	if !sleepCtx(ctx, 700*time.Millisecond) {
		return "", ctx.Err()
	}
	return id, nil
}

// Pause ставит паузу.
func (c *Client) Pause(ctx context.Context, deviceID string) error {
	return c.do(ctx, http.MethodPut, "/me/player/pause"+deviceQuery(deviceID), nil, nil)
}

// Resume снимает с паузы, не трогая, что именно играет.
//
// Отличается от PlayTrack пустым телом запроса: с телом Spotify начал бы трек
// заново, а нам нужно продолжить с того места, где остановились.
func (c *Client) Resume(ctx context.Context, deviceID string) error {
	return c.play(ctx, nil, deviceID)
}

// Next переключает Spotify на следующий трек.
//
// Нужно только для музыки самого стримера: у заказа «следующий» — это скип,
// его делает очередь, а не Spotify. Раньше этой команды не было вовсе, потому
// что своей музыкой приложение принципиально не управляло; владелец попросил
// обратное — из панели он хочет переключать и свой плейлист тоже.
//
// Ответ Spotify не разбираем: команда либо принята, либо отказ приедет из do.
func (c *Client) Next(ctx context.Context, deviceID string) error {
	return c.do(ctx, http.MethodPost, "/me/player/next"+deviceQuery(deviceID), nil, nil)
}

// Previous возвращает Spotify на предыдущий трек плейлиста стримера.
//
// Опять же только про его музыку: у очереди заказов предыдущего нет — то, что
// сыграло, из неё удаляется, и «назад» там означает перемотку в начало.
func (c *Client) Previous(ctx context.Context, deviceID string) error {
	return c.do(ctx, http.MethodPost, "/me/player/previous"+deviceQuery(deviceID), nil, nil)
}

// Seek перематывает играющий трек.
//
// Это команда, а не чтение: без запроса к Spotify перемотка невозможна в
// принципе — сдвинется только картинка в панели, а звук останется на месте.
// Поэтому один запрос на одно движение человека: панель шлёт его на отпускание
// ползунка, а не на каждый пиксель перетаскивания.
func (c *Client) Seek(ctx context.Context, positionMs int, deviceID string) error {
	if positionMs < 0 {
		positionMs = 0
	}
	path := fmt.Sprintf("/me/player/seek?position_ms=%d", positionMs)
	if deviceID != "" {
		path += "&device_id=" + url.QueryEscape(deviceID)
	}
	return c.do(ctx, http.MethodPut, path, nil, nil)
}

// SetVolume выставляет громкость устройства, на котором играет музыка.
//
// Spotify принимает только целые проценты от 0 до 100 и отвечает отказом на
// всё остальное, поэтому обрезаем здесь, а не надеемся на панель.
func (c *Client) SetVolume(ctx context.Context, percent int, deviceID string) error {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	path := fmt.Sprintf("/me/player/volume?volume_percent=%d", percent)
	if deviceID != "" {
		path += "&device_id=" + url.QueryEscape(deviceID)
	}
	return c.do(ctx, http.MethodPut, path, nil, nil)
}

// Transfer переносит воспроизведение на устройство.
func (c *Client) Transfer(ctx context.Context, deviceID string, play bool) error {
	body := map[string]any{"device_ids": []string{deviceID}, "play": play}
	return c.do(ctx, http.MethodPut, "/me/player", body, nil)
}

// SetShuffle включает или выключает перемешивание.
func (c *Client) SetShuffle(ctx context.Context, on bool, deviceID string) error {
	path := fmt.Sprintf("/me/player/shuffle?state=%t", on)
	if deviceID != "" {
		path += "&device_id=" + url.QueryEscape(deviceID)
	}
	return c.do(ctx, http.MethodPut, path, nil, nil)
}

// SetRepeat выставляет режим повтора: off, track или context.
func (c *Client) SetRepeat(ctx context.Context, mode, deviceID string) error {
	path := "/me/player/repeat?state=" + url.QueryEscape(mode)
	if deviceID != "" {
		path += "&device_id=" + url.QueryEscape(deviceID)
	}
	return c.do(ctx, http.MethodPut, path, nil, nil)
}

// PlayContext включает плейлист или альбом целиком — это запасной вариант,
// когда вернуть исходное состояние не удалось.
func (c *Client) PlayContext(ctx context.Context, contextURI, deviceID string) error {
	return c.play(ctx, &playBody{ContextURI: contextURI}, deviceID)
}

func deviceQuery(deviceID string) string {
	if deviceID == "" {
		return ""
	}
	return "?device_id=" + url.QueryEscape(deviceID)
}
