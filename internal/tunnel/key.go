// Package tunnel — обход блокировок внутри приложения.
//
// Зачем он вообще. До Spotify из России напрямую не достучаться, и раньше
// стример поднимал обход руками: ставил Psiphon, лез в его настройки, вписывал
// номер порта, потом переписывал этот же номер в нашу панель. Три программы и
// два места, где можно ошибиться, — ровно то, на что жаловался тестер.
//
// Теперь человек вставляет в приложение свой ключ (или ссылку на подписку от
// платного VPN), а всё остальное приложение делает само.
//
// Как устроено. Ключи бывают разные — vless, shadowsocks, trojan, vmess, — и
// говорить на всех этих языках умеет Xray. Мы не вшиваем его внутрь .exe:
//
//   - лицензия у него своя, и вшивание потянуло бы за собой обязательства;
//   - .exe вырос бы с 13 до 50 мегабайт, а стример качает его по обычному
//     домашнему интернету;
//   - обновляется Xray отдельно и часто — вслед за тем, как меняются
//     блокировки.
//
// Вместо этого приложение скачивает xray.exe при первом включении обхода и
// запускает его рядом с собой — точно так же, как уже делает с mpv и yt-dlp
// для заказов с YouTube. Для стримера это невидимо: никаких лишних окон, ничего
// запускать и настраивать руками не надо.
//
// Xray поднимает у себя обычный SOCKS-посредник на 127.0.0.1 и никуда больше
// не лезет. Приложение отдаёт этот адрес тому же самому коду, который до сих
// пор работал с ручным прокси (internal/spotify/proxy.go), — то есть через
// обход идут только запросы к Spotify, а Twitch, чат, донаты и выгрузка
// стрима как шли напрямую, так и идут.
package tunnel

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"songrequest/internal/errs"
)

// Server — один сервер обхода, разобранный из ключа.
//
// Поля намеренно «плоские» и общие для всех видов ключей: дальше из них
// собирается настройка для Xray (config.go), и разбирать там ещё раз чужие
// форматы не приходится.
type Server struct {
	Kind  string // vless, vmess, trojan, shadowsocks
	Label string // подпись из ключа (то, что после решётки) — показываем человеку
	Host  string
	Port  int

	// ID — то, чем сервер узнаёт клиента: uuid у vless и vmess, пароль у
	// trojan и shadowsocks. Секрет: в лог не пишем, в панели не показываем.
	ID     string
	Method string // способ шифрования у shadowsocks
	AltID  int    // alterId у старого vmess
	Cipher string // scy у vmess

	Flow    string // xtls-rprx-vision и подобное
	Net     string // как идёт соединение: tcp, ws, grpc, httpupgrade, xhttp
	Path    string
	HostHdr string // подставной адрес в заголовке (ws, httpupgrade)
	Service string // serviceName у grpc

	TLS         string // пусто, tls или reality
	SNI         string
	ALPN        []string
	Fingerprint string // под какой браузер маскируется рукопожатие
	PublicKey   string // reality: открытый ключ сервера
	ShortID     string // reality
	SpiderX     string // reality
	Insecure    bool   // не проверять сертификат — так себе идея, но встречается
}

// String — как сервер выглядит в панели и в логе. Без секретов: ни uuid, ни
// пароля здесь быть не должно.
func (s Server) String() string {
	name := s.Label
	if name == "" {
		name = s.Host
	}
	return fmt.Sprintf("%s (%s, %s:%d)", name, s.Kind, s.Host, s.Port)
}

// ParseKey разбирает то, что человек вставил в поле «ключ».
//
// Вставляют по-разному: один ключ, десяток ключей списком или содержимое
// подписки целиком — иногда как есть, иногда одной длинной строкой в base64
// (так его отдают панели VPN-сервисов). Разбираем все эти виды: человек не
// обязан знать, что именно у него в буфере обмена.
//
// Строки, которые разобрать не удалось, молча пропускаем — в подписках
// попадаются комментарии, заголовки и незнакомые виды ключей. Ошибку выдаём
// только если не разобралось вообще ничего.
func ParseKey(raw string) ([]Server, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errs.New(errs.TunnelNoKey, "Ключ не вставлен.")
	}

	text := decodeMaybeBase64(raw)

	var servers []Server
	var lastErr error
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		s, err := parseLine(line)
		if err != nil {
			lastErr = err
			continue
		}
		servers = append(servers, s)
	}

	if len(servers) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, errs.New(errs.TunnelNoKey,
			"В том, что вставлено, нет ни одного ключа. Скопируй ключ целиком — "+
				"он начинается с vless://, ss://, trojan:// или vmess://.")
	}
	return servers, nil
}

// LooksLikeSubscription — ссылка это на подписку или сам ключ.
//
// Подписка — обычный адрес http(s), по которому лежит список серверов. Платные
// сервисы дают именно её: серверы у них меняются, и ключ по такой ссылке
// обновляется сам.
func LooksLikeSubscription(raw string) bool {
	raw = strings.TrimSpace(strings.ToLower(raw))
	return strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://")
}

// SplitKey делит вставленное на ссылки подписок и всё остальное.
//
// Зачем. Раньше поле принимало либо одну ссылку, либо список ключей — решало
// это по первым буквам всей строки целиком. Но у сервисов подписки обычно не
// один адрес, а два-три: основной и запасные, на случай если основной
// заблокируют или он ляжет. Вставить их все было некуда, и падение
// единственного адреса означало вечер без обхода.
//
// Теперь строки разбираются по одной: всё, что начинается с http, — ссылки (в
// том порядке, в каком вписаны), остальное складывается обратно в текст и
// разбирается как ключи. Человеку ничего знать не нужно: вставил что есть — и
// приложение разберётся само.
//
// Строку со ссылками делим ещё и по пробелам с запятыми: поле в панели —
// однострочное (ключ секретный, поле-пароль), и вписать в него две ссылки
// можно только через пробел. С ключами так делать нельзя: у них после решётки
// стоит имя сервера, а в имени пробелы — обычное дело.
func SplitKey(raw string) (links []string, keys string) {
	sep := func(r rune) bool { return r == ' ' || r == '\t' || r == ',' || r == ';' }

	var rest []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if LooksLikeSubscription(line) {
			for _, part := range strings.FieldsFunc(line, sep) {
				if LooksLikeSubscription(part) {
					links = append(links, part)
				}
			}
			continue
		}
		rest = append(rest, line)
	}
	return links, strings.Join(rest, "\n")
}

// decodeMaybeBase64 разворачивает список серверов, если он в base64.
//
// Признак простой: в обычном списке есть «://», а в base64 его быть не может.
// Кодировок две (обычная и та, что для адресов), хвост из знаков «=» тоже
// бывает не всегда — пробуем все сочетания, ничего не угадывая.
func decodeMaybeBase64(raw string) string {
	if strings.Contains(raw, "://") {
		return raw
	}
	packed := strings.Join(strings.Fields(raw), "")
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if data, err := enc.DecodeString(packed); err == nil && strings.Contains(string(data), "://") {
			return string(data)
		}
	}
	return raw
}

// parseLine разбирает одну строку-ключ.
func parseLine(line string) (Server, error) {
	switch {
	case strings.HasPrefix(line, "vless://"):
		return parseVLESS(line)
	case strings.HasPrefix(line, "trojan://"):
		return parseTrojan(line)
	case strings.HasPrefix(line, "ss://"):
		return parseShadowsocks(line)
	case strings.HasPrefix(line, "vmess://"):
		return parseVMess(line)
	}
	return Server{}, errs.New(errs.TunnelBadKey,
		"Такой ключ приложение не понимает. Подходят vless://, ss://, trojan:// и vmess://.")
}

// parseVLESS: vless://uuid@адрес:порт?параметры#подпись
func parseVLESS(line string) (Server, error) {
	link, err := splitLink(line, "vless")
	if err != nil {
		return Server{}, err
	}
	if link.user == "" {
		return Server{}, errs.New(errs.TunnelBadKey, "В ключе vless нет опознавательного номера (uuid).")
	}
	s := Server{
		Kind:  "vless",
		Label: link.label,
		Host:  link.host,
		Port:  link.port,
		ID:    link.user,
		Flow:  link.query.Get("flow"),
	}
	applyStream(&s, link.query)
	return s, nil
}

// parseTrojan: trojan://пароль@адрес:порт?параметры#подпись
func parseTrojan(line string) (Server, error) {
	link, err := splitLink(line, "trojan")
	if err != nil {
		return Server{}, err
	}
	if link.user == "" {
		return Server{}, errs.New(errs.TunnelBadKey, "В ключе trojan нет пароля.")
	}
	s := Server{Kind: "trojan", Label: link.label, Host: link.host, Port: link.port, ID: link.user}
	applyStream(&s, link.query)
	// У trojan шифрование включено всегда, даже если в ключе про него не сказано.
	if s.TLS == "" {
		s.TLS = "tls"
	}
	return s, nil
}

// link — ключ-ссылка, разобранный на части.
type link struct {
	user  string // uuid у vless, пароль у trojan
	host  string
	port  int
	query url.Values
	label string
}

// splitLink разбирает ключ-ссылку руками, без url.Parse.
//
// Готовый разборщик адресов на такой строке спотыкается: пароль стоит там, где
// в обычной ссылке имя пользователя, и в паролях попадается всё подряд —
// кириллица, собака, проценты. url.Parse на этом отвечает «invalid userinfo» и
// объявляет ошибкой совершенно рабочий ключ. Тот же урок уже был выучен на
// адресах прокси (internal/spotify/proxy.go).
func splitLink(line, scheme string) (link, error) {
	rest := strings.TrimPrefix(line, scheme+"://")

	var out link
	if i := strings.Index(rest, "#"); i >= 0 {
		out.label = unescape(rest[i+1:])
		rest = rest[:i]
	}
	if i := strings.Index(rest, "?"); i >= 0 {
		// Разбор параметров может споткнуться об один кривой кусок — берём
		// то, что разобралось: остальные параметры ключа от этого не портятся.
		out.query, _ = url.ParseQuery(rest[i+1:])
		rest = rest[:i]
	}
	if out.query == nil {
		out.query = url.Values{}
	}

	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return link{}, errs.New(errs.TunnelBadKey, "В ключе "+scheme+" нет адреса сервера.")
	}
	out.user = unescape(rest[:at])

	host, port, err := splitHostPort(rest[at+1:])
	if err != nil {
		return link{}, err
	}
	out.host, out.port = host, port
	return out, nil
}

// parseShadowsocks разбирает обе записи ss://, которые встречаются вживую:
//
//	ss://base64(метод:пароль)@адрес:порт#подпись   — нынешняя
//	ss://base64(метод:пароль@адрес:порт)#подпись   — старая, целиком в base64
func parseShadowsocks(line string) (Server, error) {
	body := strings.TrimPrefix(line, "ss://")
	name := ""
	if i := strings.Index(body, "#"); i >= 0 {
		name = unescape(body[i+1:])
		body = body[:i]
	}
	// Всё, что после вопросительного знака (плагины), нам не пригодится:
	// плагины приложение не поддерживает и делать вид, что поддерживает, не
	// станет — иначе человек получит «подключено» и молчащую музыку.
	if i := strings.Index(body, "?"); i >= 0 {
		body = body[:i]
	}

	at := strings.LastIndex(body, "@")
	if at < 0 {
		// Старая запись: в base64 лежит вся строка целиком.
		decoded := decodeSegment(body)
		at = strings.LastIndex(decoded, "@")
		if at < 0 {
			return Server{}, errs.New(errs.TunnelBadKey, "Ключ ss:// записан непонятно.")
		}
		body = decoded
	}

	creds := decodeSegment(body[:at])
	host, port, err := splitHostPort(body[at+1:])
	if err != nil {
		return Server{}, err
	}
	colon := strings.Index(creds, ":")
	if colon < 0 {
		return Server{}, errs.New(errs.TunnelBadKey, "В ключе ss:// не разобрать способ шифрования и пароль.")
	}

	return Server{
		Kind:   "shadowsocks",
		Label:  name,
		Host:   host,
		Port:   port,
		Method: creds[:colon],
		ID:     creds[colon+1:],
	}, nil
}

// parseVMess: vmess://base64(json). Внутри — описание сервера полями,
// названия у которых короткие и не всегда очевидные (add, aid, scy, net).
func parseVMess(line string) (Server, error) {
	raw := decodeSegment(strings.TrimPrefix(line, "vmess://"))
	var v struct {
		PS   string      `json:"ps"`
		Add  string      `json:"add"`
		Port json.Number `json:"port"`
		ID   string      `json:"id"`
		Aid  json.Number `json:"aid"`
		Scy  string      `json:"scy"`
		Net  string      `json:"net"`
		Type string      `json:"type"`
		Host string      `json:"host"`
		Path string      `json:"path"`
		TLS  string      `json:"tls"`
		SNI  string      `json:"sni"`
		ALPN string      `json:"alpn"`
		FP   string      `json:"fp"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return Server{}, badKey("vmess", err)
	}
	port, _ := strconv.Atoi(v.Port.String())
	if v.Add == "" || port == 0 || v.ID == "" {
		return Server{}, errs.New(errs.TunnelBadKey, "В ключе vmess не хватает адреса или номера.")
	}
	aid, _ := strconv.Atoi(v.Aid.String())

	s := Server{
		Kind:        "vmess",
		Label:       v.PS,
		Host:        v.Add,
		Port:        port,
		ID:          v.ID,
		AltID:       aid,
		Cipher:      v.Scy,
		Net:         normalizeNet(v.Net),
		Path:        v.Path,
		HostHdr:     v.Host,
		Service:     v.Path, // у grpc имя службы записано в том же поле
		SNI:         v.SNI,
		Fingerprint: v.FP,
	}
	if v.TLS == "tls" || v.TLS == "reality" {
		s.TLS = v.TLS
	}
	if v.ALPN != "" {
		s.ALPN = strings.Split(v.ALPN, ",")
	}
	if s.Cipher == "" {
		s.Cipher = "auto"
	}
	return s, nil
}

// applyStream переносит в сервер то, как идёт соединение: эти параметры
// одинаково записаны у vless, trojan и остальных ключей-ссылок.
func applyStream(s *Server, q url.Values) {
	s.Net = normalizeNet(q.Get("type"))
	s.Path = q.Get("path")
	s.HostHdr = q.Get("host")
	s.Service = q.Get("serviceName")
	s.SNI = q.Get("sni")
	s.Fingerprint = q.Get("fp")
	s.PublicKey = q.Get("pbk")
	s.ShortID = q.Get("sid")
	s.SpiderX = q.Get("spx")

	switch security := q.Get("security"); security {
	case "tls", "reality", "xtls":
		s.TLS = security
	}
	// reality узнаётся и по своему ключу: попадаются ключи, где security не
	// указан вовсе, а pbk есть.
	if s.PublicKey != "" {
		s.TLS = "reality"
	}
	if alpn := q.Get("alpn"); alpn != "" {
		s.ALPN = strings.Split(alpn, ",")
	}
	if v := q.Get("allowInsecure"); v == "1" || v == "true" {
		s.Insecure = true
	}
	if s.SNI == "" {
		// Многие ключи имя сервера не пишут, а подставляют подставной адрес
		// из заголовка. Без имени рукопожатие уйдёт на голый IP и сорвётся.
		s.SNI = s.HostHdr
	}
}

// normalizeNet приводит запись способа соединения к тому, что понимает Xray.
func normalizeNet(net string) string {
	switch strings.ToLower(strings.TrimSpace(net)) {
	case "", "tcp", "raw":
		return "tcp"
	case "ws", "websocket":
		return "ws"
	case "grpc", "gun":
		return "grpc"
	case "httpupgrade":
		return "httpupgrade"
	case "xhttp", "splithttp":
		return "xhttp"
	case "h2", "http":
		return "http"
	case "kcp", "mkcp":
		return "kcp"
	default:
		return strings.ToLower(net)
	}
}

func unescape(s string) string {
	if out, err := url.QueryUnescape(s); err == nil {
		return out
	}
	return s
}

// decodeSegment разворачивает кусок ключа из base64, если он там есть.
func decodeSegment(s string) string {
	for _, enc := range []*base64.Encoding{
		base64.RawURLEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.StdEncoding,
	} {
		if data, err := enc.DecodeString(s); err == nil {
			return string(data)
		}
	}
	return s
}

func splitHostPort(hostPort string) (string, int, error) {
	host, portText, err := net.SplitHostPort(hostPort)
	if err != nil {
		return "", 0, errs.New(errs.TunnelBadKey, "В ключе не разобрать адрес сервера.")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, errs.New(errs.TunnelBadKey, "В ключе неверный номер порта.")
	}
	return host, port, nil
}

func badKey(kind string, err error) error {
	return errs.Wrap(errs.TunnelBadKey, "Ключ "+kind+" записан непонятно.", err)
}
