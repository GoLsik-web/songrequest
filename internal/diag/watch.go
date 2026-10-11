package diag

import (
	"context"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"

	"songrequest/internal/logx"
)

// Подробный журнал: раз в полминуты — снимок того, как живут приложение и
// компьютер.
//
// Зачем. 11.10 тестер жаловался, что с включённым приложением «тормозит
// интернет на всём компьютере». По обычному журналу это не разобрать: в нём
// решения приложения, а не то, сколько оно ест и как идёт сеть. Здесь именно
// это: память и процессор самого приложения, Xray (обход) и mpv (YouTube), и
// задержка до интернета — напрямую и через обход. Если интернет тормозит
// из-за нас, это будет видно по цифрам: растёт процессор xray, растёт
// задержка напрямую, когда обход включён.
//
// Включается переключателем «Подробный журнал» и стоит копейки: несколько
// системных вызовов и два коротких соединения раз в полминуты.

// WatchOptions — что наблюдателю надо знать о приложении.
type WatchOptions struct {
	// On — включён ли подробный журнал прямо сейчас. Спрашивается на каждом
	// шаге: переключатель действует сразу, без перезапуска.
	On func() bool
	// TunnelAddr — адрес обхода (SOCKS на 127.0.0.1) или пусто.
	TunnelAddr func() string
}

// watchStep — как часто снимать.
const watchStep = 30 * time.Second

// Watch снимает показания, пока не отменят контекст.
func Watch(ctx context.Context, log *logx.Logger, o WatchOptions) {
	t := time.NewTicker(watchStep)
	defer t.Stop()
	var prev map[string]procSample
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if o.On == nil || !o.On() {
			prev = nil
			continue
		}
		prev = snapshot(ctx, log, o, prev)
	}
}

func snapshot(ctx context.Context, log *logx.Logger, o WatchOptions, prev map[string]procSample) map[string]procSample {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	now := sampleProcs()
	args := []any{
		"горутин", runtime.NumGoroutine(),
		"память_приложения_мб", mem.Sys >> 20,
	}
	// Процессор считаем как долю за прошедшие полминуты: сама по себе
	// накопленная цифра ничего не говорит.
	for name, s := range now {
		args = append(args, name+"_память_мб", s.memMB)
		if p, ok := prev[name]; ok && s.at.After(p.at) {
			cpu := float64(s.cpu-p.cpu) / float64(s.at.Sub(p.at)) * 100
			args = append(args, name+"_процессор_%", int(cpu+0.5))
		}
		if s.count > 1 {
			args = append(args, name+"_копий", s.count)
		}
	}

	// Задержка до интернета. Одно соединение до общеизвестного адреса —
	// напрямую, и одно до Spotify через обход, если он есть. Растёт первое —
	// тормозит весь интернет; растёт только второе — тормозит сервер обхода.
	// Соединение мерим целым запросом, а не одним «открылось»: местный VPN
	// или фильтр на компьютере принимает соединение сам за ноль миллисекунд,
	// и такая цифра ничего не говорит. Запрос доходит до настоящего сайта.
	args = append(args, "сеть_напрямую_мс", httpMs(ctx, "https://www.gstatic.com/generate_204"))
	if addr := ""; o.TunnelAddr != nil {
		addr = o.TunnelAddr()
		if addr != "" {
			args = append(args, "сеть_через_обход_мс", dialMs(ctx, "api.spotify.com:443", addr))
		}
	}

	// VPN на весь компьютер — главный подозреваемый в «тормозит весь
	// интернет». Пишем всегда, даже пустым: «нет» — тоже ответ.
	vpn := strings.Join(systemVPN(), "; ")
	if vpn == "" {
		vpn = "нет"
	}
	args = append(args, "впн_на_весь_компьютер", vpn)

	log.Info("[подробно] состояние", args...)
	return now
}

// httpMs меряет полный запрос к сайту напрямую. -1 — не ответил за пять секунд.
func httpMs(ctx context.Context, url string) int {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return -1
	}
	// Свой транспорт без прокси и без памяти соединений: каждый замер — с нуля.
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return -1
	}
	resp.Body.Close()
	return int(time.Since(start).Milliseconds())
}

// dialMs меряет, за сколько открывается соединение. -1 — не открылось за
// пять секунд. socks — адрес обхода; пусто — напрямую.
func dialMs(ctx context.Context, target, socks string) int {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	start := time.Now()
	var (
		c   net.Conn
		err error
	)
	if socks == "" {
		var d net.Dialer
		c, err = d.DialContext(ctx, "tcp", target)
	} else {
		c, err = dialSocks(ctx, socks, target)
	}
	if err != nil {
		return -1
	}
	c.Close()
	return int(time.Since(start).Milliseconds())
}

// dialSocks открывает соединение через SOCKS5 без пароля — так устроен вход
// Xray у приложения (см. internal/tunnel/config.go).
func dialSocks(ctx context.Context, socks, target string) (net.Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", socks)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	}
	host, portStr, _ := net.SplitHostPort(target)
	port := 443
	if portStr != "" {
		var p int
		for _, ch := range portStr {
			p = p*10 + int(ch-'0')
		}
		port = p
	}
	buf := make([]byte, 2)
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		c.Close()
		return nil, err
	}
	if _, err := readFull(c, buf); err != nil || buf[1] != 0 {
		c.Close()
		return nil, errSocks
	}
	req := []byte{5, 1, 0, 3, byte(len(host))}
	req = append(req, host...)
	req = append(req, byte(port>>8), byte(port))
	if _, err := c.Write(req); err != nil {
		c.Close()
		return nil, err
	}
	head := make([]byte, 10)
	if _, err := readFull(c, head); err != nil || head[1] != 0 {
		c.Close()
		return nil, errSocks
	}
	return c, nil
}

type socksErr struct{}

func (socksErr) Error() string { return "обход не открыл соединение" }

var errSocks = socksErr{}

func readFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := c.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// procSample — показания одного вида процессов.
type procSample struct {
	at    time.Time
	cpu   time.Duration // всё процессорное время с запуска, сумма по копиям
	memMB uint64
	count int
}
