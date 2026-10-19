package obs

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"songrequest/internal/errs"
)

// fakeOBS — поддельный obs-websocket v5: вход по паролю и несколько запросов.
type fakeOBS struct {
	password string
	canvasW  int
	canvasH  int
	widget   string // адрес нашего источника; пусто — источника нет

	mu   sync.Mutex
	sent []map[string]any // все запросы, кроме чтения
}

func (f *fakeOBS) serve(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := r.Context()
	const salt, challenge = "c2FsdA==", "Y2hhbGxlbmdl"
	wsjson.Write(ctx, c, map[string]any{"op": 0, "d": map[string]any{
		"obsWebSocketVersion": "5.5.0", "rpcVersion": 1,
		"authentication": map[string]any{"salt": salt, "challenge": challenge}}})
	var id struct {
		Op int `json:"op"`
		D  struct {
			Auth string `json:"authentication"`
		} `json:"d"`
	}
	if wsjson.Read(ctx, c, &id) != nil || id.Op != 1 {
		return
	}
	if id.D.Auth != Auth(f.password, salt, challenge) {
		c.Close(4009, "Authentication failed.")
		return
	}
	wsjson.Write(ctx, c, map[string]any{"op": 2, "d": map[string]any{"negotiatedRpcVersion": 1}})
	for {
		var m struct {
			D struct {
				Type string         `json:"requestType"`
				ID   string         `json:"requestId"`
				Data map[string]any `json:"requestData"`
			} `json:"d"`
		}
		if wsjson.Read(ctx, c, &m) != nil {
			return
		}
		var data any
		switch m.D.Type {
		case "GetVideoSettings":
			data = map[string]any{"baseWidth": f.canvasW, "baseHeight": f.canvasH}
		case "GetSceneList":
			data = map[string]any{"currentProgramSceneName": "Игра",
				"scenes": []any{map[string]any{"sceneName": "Игра"}, map[string]any{"sceneName": "Отдых"}}}
		case "GetCurrentProgramScene":
			data = map[string]any{"currentProgramSceneName": "Игра"}
		case "GetSceneItemList":
			items := []any{
				map[string]any{"sceneItemId": 1, "sourceName": "Камера", "inputKind": "dshow_input"},
				map[string]any{"sceneItemId": 2, "sourceName": "Чат", "inputKind": "browser_source"},
			}
			if f.widget != "" {
				items = append(items, map[string]any{"sceneItemId": 7, "sourceName": "Песня", "inputKind": "browser_source"})
			}
			data = map[string]any{"sceneItems": items}
		case "GetInputSettings":
			u := "https://www.twitch.tv/popout/chat"
			if m.D.Data["inputName"] == "Песня" {
				u = f.widget
			}
			data = map[string]any{"inputKind": "browser_source", "inputSettings": map[string]any{"url": u}}
		default:
			f.mu.Lock()
			f.sent = append(f.sent, map[string]any{"type": m.D.Type, "data": m.D.Data})
			f.mu.Unlock()
			if m.D.Type == "CreateInput" {
				data = map[string]any{"sceneItemId": 42}
			}
		}
		wsjson.Write(ctx, c, map[string]any{"op": 7, "d": map[string]any{
			"requestType": m.D.Type, "requestId": m.D.ID,
			"requestStatus": map[string]any{"result": true, "code": 100},
			"responseData":  data}})
	}
}

func start(t *testing.T, f *fakeOBS) (string, int) {
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	host, p, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(p)
	return host, port
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func norm(v any) any {
	b, _ := json.Marshal(v)
	var out any
	json.Unmarshal(b, &out)
	return out
}

// 21:9 на холсте 1920×1080: источник 2560×1080, растянут на 1920×1080, в
// обеих сценах; чужой браузерный источник (чат) не тронут.
func TestFit219OnFullHD(t *testing.T) {
	f := &fakeOBS{password: "секрет", canvasW: 1920, canvasH: 1080,
		widget: "http://127.0.0.1:8977/widget"}
	host, port := start(t, f)
	c, err := Dial(ctx(t), host, port, "секрет")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	res, err := Fit(ctx(t), c, 8977, 2560, 1080)
	if err != nil {
		t.Fatal(err)
	}
	if res.Items != 2 || res.CanvasW != 1920 || res.CanvasH != 1080 {
		t.Fatalf("итог %+v", res)
	}
	tr := func(scene string) map[string]any {
		return map[string]any{"type": "SetSceneItemTransform", "data": map[string]any{
			"sceneName": scene, "sceneItemId": 7,
			"sceneItemTransform": map[string]any{
				"positionX": 0, "positionY": 0, "rotation": 0, "alignment": 5,
				"boundsType": "OBS_BOUNDS_STRETCH", "boundsAlignment": 0,
				"boundsWidth": 1920, "boundsHeight": 1080, "scaleX": 1, "scaleY": 1,
				"cropLeft": 0, "cropRight": 0, "cropTop": 0, "cropBottom": 0}}}
	}
	want := norm([]any{
		map[string]any{"type": "SetInputSettings", "data": map[string]any{
			"inputName": "Песня", "overlay": true,
			"inputSettings": map[string]any{"width": 2560, "height": 1080}}},
		tr("Игра"), tr("Отдых"),
	})
	got := norm(f.sent)
	if g, _ := json.Marshal(got); string(g) != mustS(want) {
		t.Fatalf("запросы:\n%s\nждали:\n%s", g, mustS(want))
	}
}

func mustS(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestWrongPassword(t *testing.T) {
	f := &fakeOBS{password: "верный", canvasW: 1920, canvasH: 1080}
	host, port := start(t, f)
	_, err := Dial(ctx(t), host, port, "чужой")
	if errs.CodeOf(err) != errs.OBSAuth {
		t.Fatalf("ждали OBS-02, а %v", err)
	}
	_, err = Dial(ctx(t), host, port, "")
	if errs.CodeOf(err) != errs.OBSAuth {
		t.Fatalf("без пароля ждали OBS-02, а %v", err)
	}
}

func TestUnreachable(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	_, err := Dial(ctx(t), "127.0.0.1", port, "")
	if errs.CodeOf(err) != errs.OBSUnreachable {
		t.Fatalf("ждали OBS-01, а %v", err)
	}
}

// Источника нет — OBS-03; «Добавить» создаёт его в текущей сцене и растягивает.
func TestNoSourceThenAdd(t *testing.T) {
	f := &fakeOBS{password: "p", canvasW: 2560, canvasH: 1080}
	host, port := start(t, f)
	c, err := Dial(ctx(t), host, port, "p")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := Fit(ctx(t), c, 8977, 1920, 1080); errs.CodeOf(err) != errs.OBSNoSource {
		t.Fatalf("ждали OBS-03, а %v", err)
	}
	if _, err := Add(ctx(t), c, "Заказ музыки", "http://127.0.0.1:8977/widget", 2560, 1080); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 2 || f.sent[0]["type"] != "CreateInput" || f.sent[1]["type"] != "SetSceneItemTransform" {
		t.Fatalf("запросы %v", f.sent)
	}
	cr := norm(f.sent[0]["data"]).(map[string]any)
	if cr["sceneName"] != "Игра" || cr["inputKind"] != "browser_source" {
		t.Fatalf("CreateInput %v", cr)
	}
	tr := norm(f.sent[1]["data"]).(map[string]any)["sceneItemTransform"].(map[string]any)
	if tr["boundsWidth"] != float64(2560) || tr["boundsHeight"] != float64(1080) {
		t.Fatalf("рамка %v", tr)
	}
}

func TestIsWidgetURL(t *testing.T) {
	for u, want := range map[string]bool{
		"http://127.0.0.1:8977/widget":              true,
		"http://localhost:8977/widget?layer-name=x": true,
		"http://127.0.0.1:9000/widget":              false,
		"https://example.com:8977/widget":           false,
		"http://127.0.0.1:8977/":                    false,
	} {
		if IsWidgetURL(u, 8977) != want {
			t.Errorf("%s: ждали %v", u, want)
		}
	}
}
