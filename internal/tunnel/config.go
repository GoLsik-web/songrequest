package tunnel

import (
	"encoding/json"

	"songrequest/internal/errs"
)

// Здесь разобранный ключ превращается в настройку для Xray.
//
// Настройка нужна самая скромная: один вход — обычный SOCKS на 127.0.0.1,
// один выход — сервер стримера. Ни правил маршрутизации, ни списков стран, ни
// перехвата системного трафика: приложение уводит через обход только свои
// запросы к Spotify, а решает это оно само в internal/spotify/proxy.go.

type xrayConfig struct {
	Log       xrayLog        `json:"log"`
	Inbounds  []xrayInbound  `json:"inbounds"`
	Outbounds []xrayOutbound `json:"outbounds"`
}

type xrayLog struct {
	Loglevel string `json:"loglevel"`
}

type xrayInbound struct {
	Listen   string        `json:"listen"`
	Port     int           `json:"port"`
	Protocol string        `json:"protocol"`
	Settings xraySocksIn   `json:"settings"`
	Sniffing *xraySniffOff `json:"sniffing,omitempty"`
}

type xraySocksIn struct {
	Auth string `json:"auth"`
	UDP  bool   `json:"udp"`
}

// xraySniffOff выключает подглядывание в трафик. Оно нужно только для
// маршрутизации по именам сайтов, а у нас всё идёт в один выход.
type xraySniffOff struct {
	Enabled bool `json:"enabled"`
}

type xrayOutbound struct {
	Protocol       string      `json:"protocol"`
	Settings       any         `json:"settings"`
	StreamSettings *xrayStream `json:"streamSettings,omitempty"`
}

type xrayStream struct {
	Network         string           `json:"network"`
	Security        string           `json:"security,omitempty"`
	TLSSettings     *xrayTLS         `json:"tlsSettings,omitempty"`
	RealitySettings *xrayReality     `json:"realitySettings,omitempty"`
	WSSettings      *xrayWS          `json:"wsSettings,omitempty"`
	GRPCSettings    *xrayGRPC        `json:"grpcSettings,omitempty"`
	HTTPUpgrade     *xrayHTTPUpgrade `json:"httpupgradeSettings,omitempty"`
	XHTTPSettings   *xrayHTTPUpgrade `json:"xhttpSettings,omitempty"`
}

type xrayTLS struct {
	ServerName    string   `json:"serverName,omitempty"`
	Fingerprint   string   `json:"fingerprint,omitempty"`
	ALPN          []string `json:"alpn,omitempty"`
	AllowInsecure bool     `json:"allowInsecure,omitempty"`
}

type xrayReality struct {
	ServerName  string `json:"serverName,omitempty"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"publicKey"`
	ShortID     string `json:"shortId,omitempty"`
	SpiderX     string `json:"spiderX,omitempty"`
}

type xrayWS struct {
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type xrayGRPC struct {
	ServiceName string `json:"serviceName,omitempty"`
}

type xrayHTTPUpgrade struct {
	Path string `json:"path,omitempty"`
	Host string `json:"host,omitempty"`
}

// buildConfig собирает настройку Xray: вход SOCKS на указанном порту, выход —
// на сервер из ключа.
func buildConfig(s Server, port int) ([]byte, error) {
	out, err := outbound(s)
	if err != nil {
		return nil, err
	}

	cfg := xrayConfig{
		// warning, а не info: подробный лог Xray пишет каждое соединение, а
		// нам важно только то, из-за чего обход не поднялся.
		Log: xrayLog{Loglevel: "warning"},
		Inbounds: []xrayInbound{{
			Listen:   "127.0.0.1",
			Port:     port,
			Protocol: "socks",
			// Без пароля, но и снаружи этот вход недоступен: слушаем строго
			// 127.0.0.1, как и сама панель.
			Settings: xraySocksIn{Auth: "noauth", UDP: false},
			Sniffing: &xraySniffOff{Enabled: false},
		}},
		Outbounds: []xrayOutbound{out},
	}
	return json.Marshal(cfg)
}

// outbound описывает Xray сервер стримера.
func outbound(s Server) (xrayOutbound, error) {
	stream := streamSettings(s)

	switch s.Kind {
	case "vless":
		user := map[string]any{"id": s.ID, "encryption": "none"}
		// Поток (flow) живёт только на прямом соединении с шифрованием. На
		// websocket и остальных Xray с ним просто не запустится, а человек
		// увидит «обход не включился» и ничего не поймёт.
		if s.Flow != "" && s.Net == "tcp" && (s.TLS == "reality" || s.TLS == "tls") {
			user["flow"] = s.Flow
		}
		return xrayOutbound{
			Protocol: "vless",
			Settings: map[string]any{"vnext": []any{map[string]any{
				"address": s.Host, "port": s.Port, "users": []any{user},
			}}},
			StreamSettings: stream,
		}, nil

	case "vmess":
		cipher := s.Cipher
		if cipher == "" {
			cipher = "auto"
		}
		return xrayOutbound{
			Protocol: "vmess",
			Settings: map[string]any{"vnext": []any{map[string]any{
				"address": s.Host, "port": s.Port, "users": []any{map[string]any{
					"id": s.ID, "alterId": s.AltID, "security": cipher,
				}},
			}}},
			StreamSettings: stream,
		}, nil

	case "trojan":
		return xrayOutbound{
			Protocol: "trojan",
			Settings: map[string]any{"servers": []any{map[string]any{
				"address": s.Host, "port": s.Port, "password": s.ID,
			}}},
			StreamSettings: stream,
		}, nil

	case "shadowsocks":
		return xrayOutbound{
			Protocol: "shadowsocks",
			Settings: map[string]any{"servers": []any{map[string]any{
				"address": s.Host, "port": s.Port, "method": s.Method, "password": s.ID,
			}}},
			StreamSettings: stream,
		}, nil
	}

	return xrayOutbound{}, errs.New(errs.TunnelBadKey,
		"Ключ вида «"+s.Kind+"» приложение не поддерживает.")
}

// streamSettings описывает, как идёт соединение до сервера: шифрование и
// способ доставки.
func streamSettings(s Server) *xrayStream {
	st := &xrayStream{Network: s.Net}
	if st.Network == "" {
		st.Network = "tcp"
	}

	switch s.TLS {
	case "reality":
		st.Security = "reality"
		st.RealitySettings = &xrayReality{
			ServerName:  s.SNI,
			Fingerprint: fingerprint(s),
			PublicKey:   s.PublicKey,
			ShortID:     s.ShortID,
			SpiderX:     s.SpiderX,
		}
	case "tls", "xtls":
		st.Security = "tls"
		st.TLSSettings = &xrayTLS{
			ServerName:    s.SNI,
			Fingerprint:   fingerprint(s),
			ALPN:          s.ALPN,
			AllowInsecure: s.Insecure,
		}
	}

	switch st.Network {
	case "ws":
		ws := &xrayWS{Path: s.Path}
		if s.HostHdr != "" {
			ws.Headers = map[string]string{"Host": s.HostHdr}
		}
		st.WSSettings = ws
	case "grpc":
		name := s.Service
		if name == "" {
			name = s.Path
		}
		st.GRPCSettings = &xrayGRPC{ServiceName: name}
	case "httpupgrade":
		st.HTTPUpgrade = &xrayHTTPUpgrade{Path: s.Path, Host: s.HostHdr}
	case "xhttp":
		st.XHTTPSettings = &xrayHTTPUpgrade{Path: s.Path, Host: s.HostHdr}
	}
	return st
}

// fingerprint — под какой браузер маскируется рукопожатие.
//
// Пустым его оставлять нельзя: у reality без отпечатка Xray не запускается, а
// у обычного tls соединение выглядит как «программа, а не браузер» — ровно то,
// что блокировки и ловят. chrome берут по умолчанию все клиенты.
func fingerprint(s Server) string {
	if s.Fingerprint != "" {
		return s.Fingerprint
	}
	return "chrome"
}
