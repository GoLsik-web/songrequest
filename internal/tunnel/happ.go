package tunnel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
)

// Подписки, которые отдают серверы только «своим» программам.
//
// Найдено 11.10 на подписке тестера (sub.vpnvpn.best). Сервис отвечал 502 на
// любое имя клиента, кроме Happ, а Happ без номера устройства получал
// заглушку — один сервер 0.0.0.0:1 с подписью «HWID: Не поддерживается».
// Так устроены панели с ограничением числа устройств: подписка привязывается
// к номеру устройства (заголовок x-hwid), и без него серверов не дают.
//
// Поэтому приложение представляется так же, как Happ, и присылает свой
// номер устройства — постоянный для этого компьютера, чтобы в подписке оно
// числилось одним устройством, а не новым на каждой попытке. Ответ в этом
// случае — не ключи vless://, а готовые настройки Xray в JSON, по одной на
// сервер. Из каждой берём выход на сервер как есть: переписывать его в наш
// вид и обратно — значит потерять настройки, которых мы не знаем.

// deviceHeaders — заголовки, по которым сервис подписки узнаёт устройство.
func deviceHeaders() map[string]string {
	return map[string]string{
		"x-hwid":         deviceID(),
		"x-device-os":    "Windows",
		"x-ver-os":       "10",
		"x-device-model": "songrequest",
	}
}

// deviceID — постоянный номер этого компьютера. Сам номер машины наружу не
// уходит: от него берётся отпечаток.
func deviceID() string {
	base := machineID()
	if base == "" {
		base, _ = os.Hostname()
	}
	sum := sha256.Sum256([]byte("songrequest:" + base))
	return hex.EncodeToString(sum[:])[:32]
}

// rawProtocols — какие выходы умеет наш Xray без сюрпризов. Прочие (hysteria
// и подобные) пропускаем: в старых сборках Xray их нет, и сервер просто не
// поднимется.
var rawProtocols = map[string]bool{"vless": true, "vmess": true, "trojan": true, "shadowsocks": true}

// parseXrayJSON разбирает ответ подписки в виде настроек Xray. Второе
// значение — был ли это вообще JSON.
func parseXrayJSON(body string) ([]Server, bool) {
	body = strings.TrimSpace(body)
	var configs []map[string]json.RawMessage
	switch {
	case strings.HasPrefix(body, "{"):
		var one map[string]json.RawMessage
		if json.Unmarshal([]byte(body), &one) != nil {
			return nil, false
		}
		configs = append(configs, one)
	case strings.HasPrefix(body, "["):
		if json.Unmarshal([]byte(body), &configs) != nil {
			return nil, false
		}
	default:
		return nil, false
	}

	var out []Server
	for _, c := range configs {
		var label string
		json.Unmarshal(c["remarks"], &label)
		var outs []map[string]any
		json.Unmarshal(c["outbounds"], &outs)
		for _, o := range outs {
			proto, _ := o["protocol"].(string)
			if !rawProtocols[proto] {
				continue
			}
			host, port := rawEndpoint(o)
			// Заглушка вида 0.0.0.0:1 — так сервис говорит «не дам».
			if host == "" || host == "0.0.0.0" || port <= 1 {
				continue
			}
			delete(o, "tag")
			raw, err := json.Marshal(o)
			if err != nil {
				continue
			}
			out = append(out, Server{Kind: proto, Label: label, Host: host, Port: port, Raw: raw})
			break // в одной настройке — один сервер
		}
	}
	return out, true
}

// rawEndpoint достаёт адрес сервера из выхода Xray.
func rawEndpoint(o map[string]any) (string, int) {
	settings, _ := o["settings"].(map[string]any)
	for _, key := range []string{"vnext", "servers"} {
		list, _ := settings[key].([]any)
		if len(list) == 0 {
			continue
		}
		first, _ := list[0].(map[string]any)
		host, _ := first["address"].(string)
		port, _ := first["port"].(float64)
		return host, int(port)
	}
	return "", 0
}
