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
	PositionMs  int       `json:"position_ms"`
	DurationMs  int       `json:"duration_ms"`
	IsPlaying   bool      `json:"is_playing"`
	Shuffle     bool      `json:"shuffle"`
	Repeat      string    `json:"repeat"`
}

// Describe — короткое описание снимка для панели.
func (s *Snapshot) Describe() string {
	if s == nil {
		return "снимок не снят"
	}
	if s.Empty {
		return "в момент снимка ничего не играло"
	}
	where := "без плейлиста"
	if s.ContextURI != "" {
		where = s.ContextType
	}
	return fmt.Sprintf("%s — %s (%s, %s)", s.ArtistName, s.TrackName, where, mmss(s.PositionMs))
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
	}
	if len(st.Item.Artists) > 0 {
		snap.ArtistName = st.Item.Artists[0].Name
	}
	if st.Context != nil {
		snap.ContextURI = st.Context.URI
		snap.ContextType = st.Context.Type
	}

	c.log.Info("снял снимок Spotify",
		"трек", snap.TrackName, "артист", snap.ArtistName,
		"контекст", snap.ContextURI, "позиция_мс", snap.PositionMs,
		"играл", snap.IsPlaying, "устройство", snap.DeviceName)
	return snap, nil
}

// RestoreOutcome — чем закончилась попытка вернуть всё как было.
type RestoreOutcome struct {
	Restored bool      `json:"restored"`
	Code     errs.Code `json:"code"`
	Message  string    `json:"message"`
}

// Restore возвращает Spotify в состояние из снимка.
//
// playedURI — трек, который мы играли своим заказом. Он нужен, чтобы понять,
// не переключил ли стример музыку руками: если сейчас играет что-то третье,
// снимок протух и лезть в чужое воспроизведение нельзя.
func (c *Client) Restore(ctx context.Context, snap *Snapshot, playedURI string) (RestoreOutcome, error) {
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

	if playing && current != nil && current.Item != nil && IsStale(snap, current.Item.URI, playedURI) {
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

	// Стример слушал на паузе — вернём и паузу тоже. Перемотать трек, не
	// запустив его, Spotify не даёт, поэтому пауза ставится сразу после старта.
	if !snap.IsPlaying {
		if !sleepCtx(ctx, 300*time.Millisecond) {
			return RestoreOutcome{}, ctx.Err()
		}
		if err := c.Pause(ctx, deviceID); err != nil {
			c.log.Warn("не вернул паузу", "ошибка", err)
		}
	}

	c.log.Info("вернул Spotify в исходное состояние",
		"трек", snap.TrackName, "позиция_мс", snap.PositionMs, "контекст", snap.ContextURI)

	msg := fmt.Sprintf("Вернул: %s — %s с %s.", snap.ArtistName, snap.TrackName, mmss(snap.PositionMs))
	if lostContext {
		msg += " Плейлист вернуть не вышло — Spotify не умеет возвращаться внутрь такого источника."
	}
	return RestoreOutcome{Restored: true, Message: msg}, nil
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
	if len(devices) == 0 {
		return "", errs.New(errs.SpotifyNoDevice,
			"Spotify нигде не запущен. Открой приложение Spotify на компьютере и включи любой трек.")
	}

	// Идеально — то же устройство, где играли до заказа.
	for _, d := range devices {
		if d.ID == snap.DeviceID && !d.IsRestricted {
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
// Второе значение — признак того, что вернуться внутрь исходного источника
// не выйдет. Spotify принимает «начать с этого трека» только для альбома и
// плейлиста; для радио артиста или подкаста остаётся включить сам трек.
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
	return body, snap.ContextURI != ""
}

func supportsOffset(contextURI string) bool {
	return strings.HasPrefix(contextURI, "spotify:album:") ||
		strings.HasPrefix(contextURI, "spotify:playlist:")
}

// PlayTrack включает один трек вне всякого контекста — так играются заказы.
func (c *Client) PlayTrack(ctx context.Context, trackURI, deviceID string) error {
	body := &playBody{URIs: []string{trackURI}}
	return c.do(ctx, http.MethodPut, "/me/player/play"+deviceQuery(deviceID), body, nil)
}

// Pause ставит паузу.
func (c *Client) Pause(ctx context.Context, deviceID string) error {
	return c.do(ctx, http.MethodPut, "/me/player/pause"+deviceQuery(deviceID), nil, nil)
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
	body := &playBody{ContextURI: contextURI}
	return c.do(ctx, http.MethodPut, "/me/player/play"+deviceQuery(deviceID), body, nil)
}

func deviceQuery(deviceID string) string {
	if deviceID == "" {
		return ""
	}
	return "?device_id=" + url.QueryEscape(deviceID)
}
