package spotify

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"songrequest/internal/errs"
)

// Поиск прокси на этом компьютере.
//
// Просить у стримера «адрес прокси» — значит просить его пойти и что-то
// найти. Он не пойдёт, и правильно сделает.
//
// Но искать обычно и не надо: почти любая программа для обхода блокировок
// (v2rayN, Nekoray, Hiddify, Clash, Shadowsocks и прочие) поднимает у себя
// местный прокси и слушает его на известном порту. Он работает, даже когда
// «режим VPN» в самой программе выключен, — то есть когда весь остальной
// компьютер ходит напрямую. Это ровно то, что нам нужно.
//
// Поэтому приложение просто перебирает известные порты и проверяет, доходит
// ли через них до Spotify. Стример нажимает одну кнопку.

// candidates — где обычно слушают местные прокси. Порядок имеет значение:
// первыми идут самые распространённые, чтобы поиск заканчивался быстрее.
var candidates = []struct {
	scheme string
	port   int
	who    string
}{
	{"socks5", 10808, "v2rayN"},
	{"http", 10809, "v2rayN"},
	{"socks5", 2080, "Nekoray или Hiddify"},
	{"http", 2081, "Nekoray или Hiddify"},
	{"socks5", 1080, "Psiphon или Shadowsocks"},
	{"http", 8080, "Psiphon"},
	{"http", 1081, "Shadowsocks"},
	{"socks5", 7891, "Clash"},
	{"http", 7890, "Clash"},
	{"socks5", 10800, "Outline или Sing-box"},
	{"http", 8118, "Privoxy"},
	{"http", 3128, "обычный HTTP-прокси"},
}

// Found — найденный прокси.
type Found struct {
	// Address — то, что вписывается в настройки.
	Address string
	// Who — чем он, скорее всего, поднят. Нужен, чтобы человек узнал свою
	// программу и понял, что приложение не выдумало адрес.
	Who string
}

// DetectProxy ищет рабочий прокси среди известных портов.
//
// Проверяет не «открыт ли порт», а доходит ли через него до Spotify:
// открытый порт может слушать что угодно, а нам нужен именно выход наружу.
func (c *Client) DetectProxy(ctx context.Context) (Found, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	type result struct {
		order int
		found Found
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		hits []result
	)

	for i, cand := range candidates {
		// Порт закрыт — проверять нечего, и это видно мгновенно.
		if !portOpen(cand.port) {
			continue
		}

		wg.Add(1)
		go func(order int, scheme string, port int, who string) {
			defer wg.Done()

			addr := fmt.Sprintf("%s://127.0.0.1:%d", scheme, port)
			if !c.reachesSpotify(ctx, addr) {
				return
			}
			mu.Lock()
			hits = append(hits, result{order, Found{Address: addr, Who: who}})
			mu.Unlock()
		}(i, cand.scheme, cand.port, cand.who)
	}
	wg.Wait()

	if len(hits) == 0 {
		return Found{}, errs.New(errs.SpotifyProxy,
			"Не нашёл на этом компьютере ничего, через что можно достучаться до Spotify. "+
				"Запусти свою программу для обхода блокировок и нажми поиск ещё раз.")
	}

	// Из нескольких рабочих берём тот, что выше в списке: он привычнее и,
	// как правило, быстрее.
	sort.Slice(hits, func(i, j int) bool { return hits[i].order < hits[j].order })

	best := hits[0].found
	c.log.Info("прокси найден", "адрес", best.Address, "похоже_на", best.Who,
		"всего_рабочих", len(hits))
	return best, nil
}

// portOpen проверяет, слушает ли кто-нибудь порт. Дешёвая отсечка: без неё
// каждая проверка ждала бы полный таймаут.
func portOpen(port int) bool {
	conn, err := net.DialTimeout("tcp",
		fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// reachesSpotify проверяет, доходит ли через этот прокси до Spotify.
//
// Ответ 401 — это успех: значит Spotify нас услышал и попросил ключ доступа.
// Нам нужно именно это, а не сам ответ.
func (c *Client) reachesSpotify(ctx context.Context, addr string) bool {
	u, err := ParseProxy(addr)
	if err != nil {
		return false
	}

	client := &http.Client{
		Timeout:   8 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(u)},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.spotify.com/v1/me", nil)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		c.log.Debug("прокси не подошёл", "адрес", addr, "ошибка", err)
		return false
	}
	defer resp.Body.Close()

	ok := resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusOK
	c.log.Debug("проверка прокси", "адрес", addr, "ответ", resp.StatusCode, "подошёл", ok)
	return ok
}

// DirectWorks проверяет, нужен ли прокси вообще: до Spotify может доходить и
// напрямую, и тогда мешать незачем.
func (c *Client) DirectWorks(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.spotify.com/v1/me", nil)
	if err != nil {
		return false
	}

	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusOK
}
