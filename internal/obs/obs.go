// Package obs — маленький клиент obs-websocket v5 (встроен в OBS 28+).
//
// Нужен ровно для одного: чтобы источник «Браузер» с нашим виджетом сам
// становился размером выбранного формата кадра и растягивался на весь холст
// OBS. Стример жмёт формат в панели — и больше ничего в OBS не трогает.
//
// Соединение не держим: на каждое действие открываем, делаем несколько
// запросов и закрываем. OBS может быть не запущен вовсе — приложение от
// этого не должно ни ждать, ни падать, поэтому таймауты короткие.
package obs

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"songrequest/internal/errs"
)

// Timeout — сколько вообще ждём OBS на одно действие.
const Timeout = 5 * time.Second

// Client — одно открытое соединение с OBS.
type Client struct {
	conn *websocket.Conn
	n    int
}

type message struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
}

// Auth — ответ на вызов OBS: base64(sha256(base64(sha256(пароль+соль))+вызов)).
func Auth(password, salt, challenge string) string {
	a := sha256.Sum256([]byte(password + salt))
	secret := base64.StdEncoding.EncodeToString(a[:])
	b := sha256.Sum256([]byte(secret + challenge))
	return base64.StdEncoding.EncodeToString(b[:])
}

// Dial подключается и проходит вход.
func Dial(ctx context.Context, host string, port int, password string) (*Client, error) {
	if host == "" {
		host = "127.0.0.1"
	}
	if port <= 0 {
		port = 4455
	}
	addr := "ws://" + net.JoinHostPort(host, strconv.Itoa(port))
	conn, _, err := websocket.Dial(ctx, addr, nil)
	if err != nil {
		return nil, errs.Wrap(errs.OBSUnreachable,
			"OBS не отвечает. Запусти OBS и включи в нём сервер WebSocket.", err)
	}
	conn.SetReadLimit(8 << 20)
	c := &Client{conn: conn}

	var hello struct {
		Op int `json:"op"`
		D  struct {
			RPCVersion int `json:"rpcVersion"`
			Auth       *struct {
				Challenge string `json:"challenge"`
				Salt      string `json:"salt"`
			} `json:"authentication"`
		} `json:"d"`
	}
	if err := wsjson.Read(ctx, conn, &hello); err != nil || hello.Op != 0 {
		c.Close()
		return nil, errs.Wrap(errs.OBSUnreachable,
			"OBS не отвечает. Запусти OBS и включи в нём сервер WebSocket.", err)
	}
	ident := map[string]any{"rpcVersion": 1, "eventSubscriptions": 0}
	if hello.D.Auth != nil {
		if password == "" {
			c.Close()
			return nil, errs.New(errs.OBSAuth, "OBS просит пароль. Скопируй его в OBS и вставь сюда.")
		}
		ident["authentication"] = Auth(password, hello.D.Auth.Salt, hello.D.Auth.Challenge)
	}
	if err := wsjson.Write(ctx, conn, message{Op: 1, D: mustJSON(ident)}); err != nil {
		c.Close()
		return nil, errs.Wrap(errs.OBSUnreachable,
			"OBS не отвечает. Запусти OBS и включи в нём сервер WebSocket.", err)
	}
	var got message
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		c.Close()
		// 4009 — «неверный вход»: так OBS отвечает на чужой пароль.
		if websocket.CloseStatus(err) == 4009 {
			return nil, errs.Wrap(errs.OBSAuth, "OBS не принял пароль. Скопируй его заново в OBS.", err)
		}
		return nil, errs.Wrap(errs.OBSUnreachable,
			"OBS не отвечает. Запусти OBS и включи в нём сервер WebSocket.", err)
	}
	if got.Op != 2 {
		c.Close()
		return nil, errs.New(errs.OBSUnreachable, "OBS не отвечает. Запусти OBS и включи в нём сервер WebSocket.")
	}
	return c, nil
}

// Close закрывает соединение.
func (c *Client) Close() {
	if c != nil && c.conn != nil {
		c.conn.Close(websocket.StatusNormalClosure, "")
	}
}

// RequestError — OBS отказался выполнить запрос.
type RequestError struct {
	Type    string
	Code    int
	Comment string
}

func (e *RequestError) Error() string {
	return fmt.Sprintf("obs %s: %d %s", e.Type, e.Code, e.Comment)
}

// Call отправляет запрос и ждёт ответ на него. out может быть nil.
func (c *Client) Call(ctx context.Context, typ string, data, out any) error {
	c.n++
	id := strconv.Itoa(c.n)
	req := map[string]any{"requestType": typ, "requestId": id}
	if data != nil {
		req["requestData"] = data
	}
	if err := wsjson.Write(ctx, c.conn, message{Op: 6, D: mustJSON(req)}); err != nil {
		return err
	}
	for {
		var m message
		if err := wsjson.Read(ctx, c.conn, &m); err != nil {
			return err
		}
		if m.Op != 7 {
			continue // события и прочее нам не нужны
		}
		var resp struct {
			RequestID string `json:"requestId"`
			Status    struct {
				Result  bool   `json:"result"`
				Code    int    `json:"code"`
				Comment string `json:"comment"`
			} `json:"requestStatus"`
			Data json.RawMessage `json:"responseData"`
		}
		if err := json.Unmarshal(m.D, &resp); err != nil {
			return err
		}
		if resp.RequestID != id {
			continue
		}
		if !resp.Status.Result {
			return &RequestError{Type: typ, Code: resp.Status.Code, Comment: resp.Status.Comment}
		}
		if out != nil && len(resp.Data) > 0 {
			return json.Unmarshal(resp.Data, out)
		}
		return nil
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// IsWidgetURL — смотрит ли источник на наш виджет: адрес /widget на этом
// компьютере и на нашем порту.
func IsWidgetURL(raw string, port int) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.HasPrefix(u.Path, "/widget") {
		return false
	}
	h := u.Hostname()
	if h != "127.0.0.1" && h != "localhost" {
		return false
	}
	p := u.Port()
	if p == "" {
		p = "80"
	}
	return p == strconv.Itoa(port)
}

// Result — что получилось подогнать.
type Result struct {
	CanvasW, CanvasH int
	Items            int // сколько элементов сцен растянуто
	Scene            string
}

type sceneItem struct {
	ID        int    `json:"sceneItemId"`
	Source    string `json:"sourceName"`
	InputKind string `json:"inputKind"`
	IsGroup   bool   `json:"isGroup"`
}

// Canvas — базовое разрешение холста OBS.
func Canvas(ctx context.Context, c *Client) (int, int, error) {
	var v struct {
		W int `json:"baseWidth"`
		H int `json:"baseHeight"`
	}
	if err := c.Call(ctx, "GetVideoSettings", nil, &v); err != nil {
		return 0, 0, wrapReq(err)
	}
	return v.W, v.H, nil
}

// Fit находит во всех сценах источники с нашим виджетом, ставит им размер
// w×h и растягивает на весь холст. Не нашлось ни одного — ошибка OBS-03.
func Fit(ctx context.Context, c *Client, port, w, h int) (Result, error) {
	var res Result
	cw, ch, err := Canvas(ctx, c)
	if err != nil {
		return res, err
	}
	res.CanvasW, res.CanvasH = cw, ch

	var scenes struct {
		Current string `json:"currentProgramSceneName"`
		Scenes  []struct {
			Name string `json:"sceneName"`
		} `json:"scenes"`
	}
	if err := c.Call(ctx, "GetSceneList", nil, &scenes); err != nil {
		return res, wrapReq(err)
	}
	res.Scene = scenes.Current

	ours := map[string]bool{} // имя источника → наш ли он
	sized := map[string]bool{}
	for _, sc := range scenes.Scenes {
		var list struct {
			Items []sceneItem `json:"sceneItems"`
		}
		if err := c.Call(ctx, "GetSceneItemList", map[string]any{"sceneName": sc.Name}, &list); err != nil {
			return res, wrapReq(err)
		}
		for _, it := range list.Items {
			if it.IsGroup || it.InputKind != "browser_source" {
				continue
			}
			mine, seen := ours[it.Source]
			if !seen {
				var s struct {
					Settings struct {
						URL string `json:"url"`
					} `json:"inputSettings"`
				}
				if err := c.Call(ctx, "GetInputSettings", map[string]any{"inputName": it.Source}, &s); err != nil {
					return res, wrapReq(err)
				}
				mine = IsWidgetURL(s.Settings.URL, port)
				ours[it.Source] = mine
			}
			if !mine {
				continue
			}
			if !sized[it.Source] {
				if err := SetSize(ctx, c, it.Source, w, h); err != nil {
					return res, err
				}
				sized[it.Source] = true
			}
			if err := Stretch(ctx, c, sc.Name, it.ID, cw, ch); err != nil {
				return res, err
			}
			res.Items++
		}
	}
	if res.Items == 0 {
		return res, errs.New(errs.OBSNoSource, "В OBS нет источника с виджетом. Нажми «Добавить в OBS».")
	}
	return res, nil
}

// SetSize ставит источнику «Браузер» ширину и высоту страницы.
func SetSize(ctx context.Context, c *Client, input string, w, h int) error {
	return wrapReq(c.Call(ctx, "SetInputSettings", map[string]any{
		"inputName":     input,
		"inputSettings": map[string]any{"width": w, "height": h},
		"overlay":       true,
	}, nil))
}

// Stretch растягивает элемент сцены на весь холст cw×ch: от левого верхнего
// угла, рамка «растянуть», без масштаба, поворота и обрезки.
func Stretch(ctx context.Context, c *Client, scene string, id, cw, ch int) error {
	return wrapReq(c.Call(ctx, "SetSceneItemTransform", map[string]any{
		"sceneName":   scene,
		"sceneItemId": id,
		"sceneItemTransform": map[string]any{
			"positionX": 0, "positionY": 0, "rotation": 0,
			"alignment":       5, // левый верхний угол
			"boundsType":      "OBS_BOUNDS_STRETCH",
			"boundsAlignment": 0,
			"boundsWidth":     cw, "boundsHeight": ch,
			"scaleX": 1, "scaleY": 1,
			"cropLeft": 0, "cropRight": 0, "cropTop": 0, "cropBottom": 0,
		},
	}, nil))
}

// Add создаёт источник «Браузер» с виджетом в текущей сцене эфира и
// растягивает его на весь холст.
func Add(ctx context.Context, c *Client, name, widgetURL string, w, h int) (Result, error) {
	var res Result
	cw, ch, err := Canvas(ctx, c)
	if err != nil {
		return res, err
	}
	res.CanvasW, res.CanvasH = cw, ch
	var cur struct {
		Name string `json:"currentProgramSceneName"`
	}
	if err := c.Call(ctx, "GetCurrentProgramScene", nil, &cur); err != nil {
		return res, wrapReq(err)
	}
	res.Scene = cur.Name
	var created struct {
		ID int `json:"sceneItemId"`
	}
	err = c.Call(ctx, "CreateInput", map[string]any{
		"sceneName":        cur.Name,
		"inputName":        name,
		"inputKind":        "browser_source",
		"inputSettings":    map[string]any{"url": widgetURL, "width": w, "height": h},
		"sceneItemEnabled": true,
	}, &created)
	var re *RequestError
	if errors.As(err, &re) && re.Code == 601 { // имя занято
		name += " " + time.Now().Format("150405")
		err = c.Call(ctx, "CreateInput", map[string]any{
			"sceneName": cur.Name, "inputName": name, "inputKind": "browser_source",
			"inputSettings":    map[string]any{"url": widgetURL, "width": w, "height": h},
			"sceneItemEnabled": true,
		}, &created)
	}
	if err != nil {
		return res, wrapReq(err)
	}
	if err := Stretch(ctx, c, cur.Name, created.ID, cw, ch); err != nil {
		return res, err
	}
	res.Items = 1
	return res, nil
}

func wrapReq(err error) error {
	if err == nil {
		return nil
	}
	var re *RequestError
	if errors.As(err, &re) {
		return errs.Wrap(errs.OBSRequest, "OBS не выполнил команду. Подробности в логе.", err)
	}
	return errs.Wrap(errs.OBSUnreachable, "Связь с OBS оборвалась. Попробуй ещё раз.", err)
}
