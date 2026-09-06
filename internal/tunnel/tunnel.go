package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"songrequest/internal/errs"
	"songrequest/internal/logx"
)

const (
	// startTimeout — сколько ждём, пока Xray поднимет свой вход. Он стартует за
	// доли секунды; если не успел — что-то не так с настройкой, и ждать дольше
	// незачем.
	startTimeout = 5 * time.Second

	// probeTimeout — сколько ждём ответа Spotify через поднятый обход.
	//
	// Живьём на подписке владельца: годный сервер отвечает меньше чем за
	// секунду, а негодный молчит до конца срока. Поэтому срок короткий —
	// он целиком уходит на ожидание тех, кто не ответит никогда, а таких в
	// подписке большинство.
	probeTimeout = 6 * time.Second

	// maxAttempts — сколько серверов из подписки пробуем, прежде чем сдаться.
	//
	// Было восемь, и это оказалось мало: из двадцати одного сервера в
	// подписке владельца до Spotify доходят семь, и они разбросаны по всему
	// списку. Восемь попыток вполне могли кончиться на негодных, а человек
	// получил бы «ни один сервер не ответил» при рабочей подписке. Перебор
	// всё равно ограничен сверху общим сроком (пять минут в панели).
	maxAttempts = 25

	// dialTimeout — сколько ждём, пока сервер вообще отзовётся на стук в порт.
	// Мёртвые серверы в подписках попадаются пачками, и запускать ради каждого
	// программу обхода, а потом ещё десять секунд ждать Spotify — значит
	// заставить человека сидеть перед крутящимся кружком минуты.
	dialTimeout = 2500 * time.Millisecond

	// subTimeout — сколько ждём ответа от сервиса подписки на одну попытку.
	//
	// Было тридцать секунд одной попыткой. Живьём это худший из вариантов:
	// сервис подписки либо отвечает за секунду, либо не отвечает вовсе, а
	// человек всё это время смотрит на «включаю обход…». Лучше короткий срок
	// и несколько попыток: временная потеря связи так переживается, а
	// зависший сервис не съедает полминуты.
	subTimeout = 12 * time.Second

	// badFor — сколько сервер числится непригодным после отказа Spotify.
	//
	// Насовсем помечать нельзя: «Spotify не работает из этой страны» —
	// причина не вечная. Сервис подписки переставляет серверы между
	// дата-центрами, а Spotify пересматривает свои списки. Полчаса — это
	// «не суйся туда сегодня вечером», а не «забудь навсегда»: за это время
	// приложение успеет обойти остальные серверы и вернуться.
	badFor = 30 * time.Minute
)

// subRetries — паузы перед повторами обращения к сервису подписки.
//
// Повторяем только тогда, когда дело в связи (сервис молчит, оборвалось,
// ответил пятисоткой). Если он ответил внятно, но не тем, повторять бесполезно
// — ответ будет тот же.
var subRetries = []time.Duration{0, 2 * time.Second, 6 * time.Second}

// probeURL — по чему проверяем, что обход рабочий. Отвечает «нужен вход»
// (401) и без всякого ключа, зато отвечает только тем, кого пускает.
//
// Переменная, а не константа, только ради проверок: в них вместо Spotify
// отвечает поддельный сервер.
var probeURL = "https://api.spotify.com/v1/me"

// ServerCache — где приложение помнит список серверов между запусками.
//
// Зачем это нужно. Список живёт у сервиса подписки, и сервис этот — обычный
// сайт: он падает, у него кончается домен, его блокируют. Раньше в такой
// вечер обхода не было вовсе, хотя вчерашние серверы никуда не делись и
// прекрасно работали.
//
// Почему интерфейс, а не файл рядом с xray.exe. В списке лежат uuid и пароли
// от VPN стримера — то есть ровно тот секрет, который во всём остальном
// приложении на диск не попадает: сама настройка отдаётся Xray через
// стандартный ввод именно поэтому. Класть его в JSON рядом с программой
// значило бы отменить эту осторожность. Поэтому хранилище передают снаружи, а
// снаружи это хранилище паролей Windows.
type ServerCache interface {
	Save(servers []Server) error
	Load() ([]Server, bool)
}

// Tunnel — обход блокировок: запущенный рядом Xray и адрес его посредника.
type Tunnel struct {
	log *logx.Logger
	dir string // папка для xray.exe

	mu     sync.Mutex
	cmd    *exec.Cmd
	addr   string // socks5://127.0.0.1:порт
	server string // подпись рабочего сервера, для панели
	onDown func() // кому сказать, что обход отвалился сам

	// says — что программа обхода писала о себе, пока работала.
	//
	// Раньше её ругань собиралась только на время запуска, а потом
	// выбрасывалась. Живьём 31.08 это вышло боком: обход проработал сутки и
	// умер, а в логе осталось одно «ошибка=exit status 1» — ни причины, ни
	// зацепки. Теперь последние слова программы попадают в лог.
	says *safeBuf

	// bad — серверы, через которые Spotify отказался работать уже после
	// того, как обход поднялся, и когда это случилось.
	//
	// Проверка при выборе сервера ходит без ключа доступа и видит не всё:
	// бывает, что Spotify отвечает обычному запросу, а на запрос с входом
	// говорит «Spotify недоступен в этой стране». Такой сервер надо
	// запомнить и больше не предлагать — иначе приложение выбирает его
	// снова и снова, а человек видит «обход работает» при неработающем
	// Spotify.
	//
	// Время нужно, чтобы пометка сама истекала: см. badFor.
	bad map[string]time.Time

	// cache — где помнить список серверов между запусками, см. ServerCache.
	// Пусто — не помним нигде, и это не поломка.
	cache ServerCache

	// fromCache — нынешний список серверов взят из памяти, а не от сервиса
	// подписки. Человеку это стоит сказать: серверы могли устареть.
	fromCache bool
}

// MarkBad помечает сервер как непригодный: обход через него поднимается, а
// Spotify всё равно отказывает.
func (t *Tunnel) MarkBad(label string) {
	if label == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.bad == nil {
		t.bad = map[string]time.Time{}
	}
	t.bad[label] = time.Now()
}

// Bad — копия пометок вместе со временем. Нужна тому, кто сохраняет их между
// запусками: перезапуск приложения не делает негодный сервер годным, а раньше
// список забывался целиком, и вечер начинался с тех же самых граблей.
func (t *Tunnel) Bad() map[string]time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]time.Time, len(t.bad))
	for k, at := range t.bad {
		if time.Since(at) > badFor {
			continue
		}
		out[k] = at
	}
	return out
}

// SetBad возвращает пометки, сохранённые в прошлый запуск. Просроченные
// отбрасываются здесь же: нести их дальше незачем.
func (t *Tunnel) SetBad(m map[string]time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bad = make(map[string]time.Time, len(m))
	for k, at := range m {
		if time.Since(at) > badFor {
			continue
		}
		t.bad[k] = at
	}
}

// badList — какие серверы сейчас считаются непригодными.
//
// Заодно чистит просроченные пометки: полчаса прошло — сервер снова обычный.
func (t *Tunnel) badList() map[string]bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]bool, len(t.bad))
	for k, at := range t.bad {
		if time.Since(at) > badFor {
			delete(t.bad, k)
			continue
		}
		out[k] = true
	}
	return out
}

// SetOnDown говорит, кого разбудить, если обход упал сам по себе. Панели это
// нужно, чтобы вернуть Spotify на прямой путь и написать об этом человеку:
// иначе запросы продолжат уходить в мёртвого посредника, а в панели будет
// написано «Обход работает».
func (t *Tunnel) SetOnDown(f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onDown = f
}

// New создаёт обход. Сам по себе он ничего не делает, пока не позовут Start.
func New(log *logx.Logger, dataDir string) *Tunnel {
	return &Tunnel{log: log, dir: filepath.Join(dataDir, "tools")}
}

// SetCache говорит, где помнить список серверов между запусками.
func (t *Tunnel) SetCache(c ServerCache) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cache = c
}

// remember кладёт свежий список в память. Ошибки только в лог: не сумели
// запомнить — обход всё равно работает, просто в следующий раз придётся снова
// идти к сервису подписки.
func (t *Tunnel) remember(servers []Server) {
	t.mu.Lock()
	c := t.cache
	t.mu.Unlock()
	if c == nil || len(servers) == 0 {
		return
	}
	if err := c.Save(servers); err != nil {
		t.log.Warn("не запомнил список серверов обхода", "ошибка", err)
	}
}

// recall достаёт список, запомненный в прошлый раз.
func (t *Tunnel) recall() ([]Server, bool) {
	t.mu.Lock()
	c := t.cache
	t.mu.Unlock()
	if c == nil {
		return nil, false
	}
	servers, ok := c.Load()
	return servers, ok && len(servers) > 0
}

// Status — что показывать в панели.
type Status struct {
	On     bool   `json:"on"`
	Addr   string `json:"addr"`   // адрес посредника, без секретов
	Server string `json:"server"` // подпись сервера из ключа
	// FromCache — список серверов взят из памяти, а не от сервиса подписки.
	FromCache bool `json:"from_cache"`
}

// Status отвечает, работает ли обход прямо сейчас.
func (t *Tunnel) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Status{On: t.addr != "", Addr: t.addr, Server: t.server, FromCache: t.fromCache}
}

// Addr — адрес посредника для internal/spotify. Пусто, если обход выключен.
func (t *Tunnel) Addr() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.addr
}

// Start поднимает обход по ключу и возвращает адрес посредника.
//
// key — то, что человек вставил в настройках: ключ, список ключей или ссылка
// на подписку. prefer — подпись сервера, который сработал в прошлый раз: с
// него и начинаем, чтобы каждый запуск не перебирал подписку заново.
// toolPath — путь к xray.exe, если человек положил его руками.
//
// Прошлый запущенный обход гасится: двух сразу быть не должно.
func (t *Tunnel) Start(ctx context.Context, key, prefer, toolPath string) (Status, error) {
	servers, cached, err := t.resolve(ctx, key)
	if err != nil {
		return Status{}, err
	}
	tool, err := t.ensureTool(ctx, toolPath)
	if err != nil {
		return Status{}, err
	}

	t.Stop()
	order := sortPreferred(servers, prefer, t.badList())

	var lastErr error
	for i, s := range order {
		if i >= maxAttempts {
			break
		}
		if err := ctx.Err(); err != nil {
			return Status{}, err
		}

		t.log.Info("пробую сервер обхода", "сервер", s.String(), "попытка", i+1)
		if err := reachable(ctx, s); err != nil {
			lastErr = err
			t.log.Warn("сервер обхода не отвечает", "сервер", s.String(), "ошибка", err)
			continue
		}

		addr, cmd, says, err := t.launch(ctx, tool, s)
		if err != nil {
			lastErr = err
			t.log.Warn("сервер обхода не запустился", "сервер", s.String(), "ошибка", err)
			continue
		}

		if err := probe(ctx, addr); err != nil {
			lastErr = err
			t.log.Warn("через сервер обхода Spotify не ответил", "сервер", s.String(), "ошибка", err)
			killAndReap(cmd)
			continue
		}

		t.mu.Lock()
		t.cmd, t.addr, t.server, t.says = cmd, addr, s.String(), says
		t.fromCache = cached
		t.mu.Unlock()

		go t.watch(cmd, says, s.String())
		t.log.Info("обход работает", "сервер", s.String(), "посредник", addr)
		return t.Status(), nil
	}

	if lastErr != nil {
		return Status{}, lastErr
	}
	return Status{}, errs.New(errs.TunnelCheck,
		"Ни один сервер из ключа не ответил. Проверь, не кончилась ли подписка у VPN.")
}

// Stop гасит обход. Звать можно сколько угодно раз.
func (t *Tunnel) Stop() {
	t.mu.Lock()
	cmd := t.cmd
	t.cmd, t.addr, t.server, t.says = nil, "", "", nil
	t.fromCache = false
	t.mu.Unlock()

	if cmd != nil {
		t.log.Info("выключаю обход")
		kill(cmd)
	}
}

// subscriptionAgents — под каким именем спрашиваем список серверов.
//
// Сервисы подписки отдают разное в зависимости от того, кто спрашивает: тому,
// кого они узнали, — настройку в его собственном виде, всем остальным — простой
// список ключей, который нам и нужен.
//
// Найдено живьём на подписке владельца: по имени «v2rayNG» его сервис отдал 43
// килобайта настройки Xray в JSON, и приложение честно ответило «такой ключ я
// не понимаю» (OB-02). Тому же адресу под любым другим именем тот же сервис
// отдаёт ровно то, что нужно: два десятка ключей vless://, упакованных в
// base64. Поэтому имён несколько, и берётся первое, из ответа на которое
// получился список ключей.
//
// Порядок такой: сначала имена, на которые сервисы отвечают простым списком, и
// только потом «v2rayNG» — на случай сервиса, который отдаёт список одним лишь
// узнаваемым клиентам.
var subscriptionAgents = []string{
	"v2rayN/6.23",
	"Shadowrocket/1.0",
	"v2rayNG/1.8.0",
}

// resolve превращает то, что вставил человек, в список серверов.
// resolve превращает то, что вставил человек, в список серверов.
//
// Второе возвращаемое значение — «список взят из памяти». Это не ошибка, но
// сказать об этом человеку надо: серверы могли устареть.
//
// Порядок такой: сначала ссылки на подписки по очереди (первая ответившая
// выигрывает), потом ключи, вписанные прямо в поле, и только если не вышло
// ничего — список, запомненный в прошлый раз.
func (t *Tunnel) resolve(ctx context.Context, key string) ([]Server, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, false, errs.New(errs.TunnelNoKey, "Ключ обхода не вставлен.")
	}

	links, rest := SplitKey(key)

	var lastErr error
	for i, link := range links {
		servers, err := t.fromSubscription(ctx, link, i+1, len(links))
		if err != nil {
			// Зеркало не ответило — пробуем следующее. Ради этого список
			// ссылок и заведён: сервисы подписок держат по два-три адреса
			// именно потому, что один из них рано или поздно ложится.
			lastErr = err
			continue
		}
		t.remember(servers)
		return servers, false, nil
	}

	// Ключи, вписанные прямо в поле. Их не надо ни у кого спрашивать, поэтому
	// они и идут после ссылок: свежий список от сервиса лучше вписанного
	// руками, а вписанный руками лучше, чем ничего.
	if rest != "" {
		servers, err := ParseKey(rest)
		if err == nil {
			t.remember(servers)
			return servers, false, nil
		}
		if lastErr == nil {
			lastErr = err
		}
	}

	// Не ответил никто. Прошлый список — лучшее, что у нас есть: сервисы
	// подписок падают, а серверы из вчерашнего списка обычно живы. Раньше в
	// такой вечер обхода не было вовсе.
	if servers, ok := t.recall(); ok {
		t.log.Warn("сервис подписки не ответил — беру список, запомненный в прошлый раз",
			"сколько", len(servers))
		return servers, true, nil
	}

	if lastErr != nil {
		return nil, false, lastErr
	}
	return nil, false, errs.New(errs.TunnelSubscription,
		"Не понял, что вставлено в поле ключа: ни ссылки на подписку, ни ключа.")
}

// fromSubscription скачивает список по одной ссылке, с повторами.
//
// Повторяем только тогда, когда дело в связи. Если сервис ответил внятно, но
// не тем (не та ссылка, кончилась подписка), повторять бесполезно — ответ
// будет тот же, а человек лишнюю минуту смотрит на «включаю обход…».
func (t *Tunnel) fromSubscription(ctx context.Context, link string, n, total int) ([]Server, error) {
	if total > 1 {
		t.log.Info("скачиваю список серверов по ссылке подписки", "ссылка", n, "всего", total)
	} else {
		t.log.Info("скачиваю список серверов по ссылке подписки")
	}

	var lastErr error
	for attempt, pause := range subRetries {
		if pause > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(pause):
			}
			t.log.Info("пробую сервис подписки ещё раз", "попытка", attempt+1)
		}

		trouble := false
		for _, agent := range subscriptionAgents {
			body, retry, err := t.fetchSubscription(ctx, link, agent)
			if err != nil {
				// Отказ может зависеть от имени: тот же сервис владельца на
				// имя браузера отвечает «502». Поэтому пробуем следующее, а не
				// сдаёмся.
				lastErr = err
				trouble = trouble || retry
				t.log.Warn("сервис подписки не отдал список", "клиент", agent, "ошибка", err)
				continue
			}

			servers, err := ParseKey(string(body))
			if err != nil {
				lastErr = errs.New(errs.TunnelSubscription,
					"Сервис подписки ответил не списком ключей. Попробуй взять в личном "+
						"кабинете ссылку для v2rayN или Shadowrocket.")
				t.log.Warn("в ответе сервиса подписки нет ключей",
					"клиент", agent, "байт", len(body), "ошибка", err)
				continue
			}

			t.log.Info("список серверов получен", "клиент", agent, "сколько", len(servers))
			return servers, nil
		}

		if !trouble {
			break
		}
	}
	return nil, lastErr
}

// fetchSubscription скачивает список серверов от имени одного клиента.
//
// Второе возвращаемое значение — «стоит ли повторить»: да, если дело в связи
// или в самом сервисе (пятисотка), и нет, если он ответил внятным отказом.
func (t *Tunnel) fetchSubscription(ctx context.Context, url, agent string) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, errs.Wrap(errs.TunnelSubscription, "Ссылка на подписку записана непонятно.", err)
	}
	req.Header.Set("User-Agent", agent)

	resp, err := (&http.Client{Timeout: subTimeout}).Do(req)
	if err != nil {
		return nil, true, errs.Wrap(errs.TunnelSubscription,
			"Не получилось скачать список серверов по ссылке. Проверь интернет и саму ссылку.", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode >= 500, errs.New(errs.TunnelSubscription,
			fmt.Sprintf("Сервис подписки ответил %d. Проверь ссылку и не кончилась ли подписка.", resp.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, true, errs.Wrap(errs.TunnelSubscription, "Список серверов не дочитался.", err)
	}
	return body, false, nil
}

// launch запускает Xray с настройкой под один сервер и ждёт, пока поднимется
// вход посредника.
func (t *Tunnel) launch(ctx context.Context, tool string, s Server) (string, *exec.Cmd, *safeBuf, error) {
	port, err := freePort()
	if err != nil {
		return "", nil, nil, errs.Wrap(errs.TunnelStart, "Не нашёл свободный порт для обхода.", err)
	}
	cfg, err := buildConfig(s, port)
	if err != nil {
		return "", nil, nil, err
	}

	// Настройку отдаём через стандартный ввод, а не файлом.
	//
	// В ней лежит опознавательный номер стримера (по сути пароль от его VPN), и
	// класть его на диск открытым текстом незачем: сам ключ приложение хранит
	// в хранилище паролей Windows, а не в настройках.
	cmd := exec.Command(tool, "run", "-c", "stdin:")
	cmd.Stdin = strings.NewReader(string(cfg))
	hideWindow(cmd)

	// Ругань Xray забираем себе: это единственное место, где будет написано,
	// чем именно ему не понравился ключ — и почему он потом умер.
	says := &safeBuf{}
	cmd.Stdout = says
	cmd.Stderr = says

	if err := cmd.Start(); err != nil {
		return "", nil, nil, errs.Wrap(errs.TunnelStart, "Программа обхода не запустилась.", err)
	}

	addr := fmt.Sprintf("socks5://127.0.0.1:%d", port)
	if err := waitPort(ctx, port); err != nil {
		killAndReap(cmd)
		if text := says.String(); text != "" {
			t.log.Warn("программа обхода отказалась работать", "ответ", lastLines(text, 3))
		}
		return "", nil, nil, errs.Wrap(errs.TunnelStart,
			"Обход не поднялся: программа не приняла ключ.", err)
	}
	return addr, cmd, says, nil
}

// safeBuf — то, что программа обхода пишет о себе, с двумя оговорками.
//
// Первая: пишет в него чужая горутина (её заводит os/exec), а читаем мы из
// своей — без замка это гонка, а гонки в Go кончаются не «неточным текстом», а
// падением всего приложения.
//
// Вторая: держим только хвост. Xray, обиженный на сеть, может писать по строке
// в секунду сутки подряд, и без ограничения приложение съело бы всю память
// стримера ради текста, из которого нужны последние три строки.
type safeBuf struct {
	mu   sync.Mutex
	text []byte
}

// maxSays — сколько последних байт ругани храним.
const maxSays = 8 << 10

func (b *safeBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.text = append(b.text, p...)
	if len(b.text) > maxSays {
		b.text = b.text[len(b.text)-maxSays:]
	}
	return len(p), nil
}

func (b *safeBuf) String() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.text))
}

// watch следит за тем, что обход не умер сам.
//
// Xray может выйти в любой момент — например, если сервер разорвал связь или
// у стримера кончилась подписка. Молчать об этом нельзя: заказы начнут падать
// с «Spotify не отвечает», и искать причину будут не там.
func (t *Tunnel) watch(cmd *exec.Cmd, says *safeBuf, server string) {
	err := cmd.Wait()

	t.mu.Lock()
	ours := t.cmd == cmd
	if ours {
		t.cmd, t.addr, t.server, t.says = nil, "", "", nil
	}
	onDown := t.onDown
	t.mu.Unlock()

	if ours {
		// Последние слова программы обхода — единственная зацепка, почему она
		// вышла. Без них в логе оставалось «exit status 1», и понять, кончилась
		// подписка или сервер разорвал связь, было нельзя.
		t.log.Warn("обход остановился сам", "сервер", server, "ошибка", err,
			"последние_слова", lastLines(says.String(), 3))
		if onDown != nil {
			onDown()
		}
	}
}

// Alive проверяет, что посредник ещё принимает соединения.
//
// Смерть программы обхода ловится сама (см. watch), но бывает и хуже: процесс
// жив, а вход посредника уже никого не пускает. Тогда приложение уверено, что
// обход работает, а Spotify молчит — и человек ищет причину не там. Проверка
// местная и бесплатная: стучимся в свой же порт, наружу не ходим.
func (t *Tunnel) Alive() bool {
	addr := t.Addr()
	if addr == "" {
		return false
	}
	host := strings.TrimPrefix(addr, "socks5://")
	conn, err := net.DialTimeout("tcp", host, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// probe проверяет, что через посредника Spotify отвечает — и отвечает так, как
// нужно.
//
// Мало дойти до Spotify: он смотрит, откуда пришли. С адреса, который он
// считает неподходящей страной (а такими бывают целые дата-центры, где стоят
// серверы обхода), он отвечает 403 «Spotify is unavailable in this country» —
// и тогда не работает ничего: ни вход, ни заказы.
//
// Найдено живьём 30.08: обход поднялся, в панели было написано «Обход
// работает», а приложение не могло даже запомнить играющий трек. Проверка
// принимала любой ответ, поэтому выбирала первый попавшийся сервер, в том
// числе такой.
//
// Хороший ответ — 401 «нужен вход»: значит Spotify видит обычного клиента и
// готов с ним разговаривать. 429 тоже годится: он нас узнал и просто просит
// сбавить темп.
func probe(ctx context.Context, addr string) error { return CheckSpotify(ctx, addr) }

// CheckSpotify — та же проверка, но её можно позвать и снаружи, и без
// посредника: пустой addr означает «спроси Spotify напрямую».
//
// Прямая проверка нужна, чтобы приложение понимало, нужен ли обход вообще.
// У стримера может быть включён свой VPN на весь компьютер — тогда Spotify
// отвечает и так, а обход поверх VPN превращает каждый запрос в путь через два
// туннеля подряд. Живьём 31.08 это выглядело так: напрямую Spotify отвечал за
// 0,17 секунды, через обход поверх включённого VPN — за 19,5, при том что
// приложение ждёт ответа 12 и считает такое молчанием.
func CheckSpotify(ctx context.Context, addr string) error {
	transport := &http.Transport{TLSHandshakeTimeout: probeTimeout}
	if addr != "" {
		u, err := url.Parse(addr)
		if err != nil {
			return errs.Wrap(errs.TunnelCheck, "Внутренняя ошибка адреса посредника.", err)
		}
		transport.Proxy = http.ProxyURL(u)
	}
	client := &http.Client{Timeout: probeTimeout, Transport: transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return errs.Wrap(errs.TunnelCheck, "Через обход Spotify не отвечает.", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	switch {
	case resp.StatusCode == http.StatusForbidden ||
		strings.Contains(string(body), "unavailable in this country"):
		return errs.New(errs.TunnelCountry,
			"Через этот сервер Spotify не работает: он считает страну сервера неподходящей.")
	case resp.StatusCode >= 500:
		return errs.New(errs.TunnelCheck,
			fmt.Sprintf("Через этот сервер Spotify отвечает ошибкой %d.", resp.StatusCode))
	}
	return nil
}

// reachable стучится в порт сервера до всякого запуска программы обхода.
//
// Это не проверка обхода: сервер может отвечать на стук и при этом не пускать
// дальше. Зато молчащий порт означает наверняка мёртвый сервер, и на него не
// стоит тратить полтора десятка секунд.
func reachable(ctx context.Context, s Server) error {
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(s.Host, strconv.Itoa(s.Port)))
	if err != nil {
		return errs.Wrap(errs.TunnelCheck, "Сервер обхода не отвечает.", err)
	}
	conn.Close()
	return nil
}

// sortPreferred ставит первым сервер, который сработал в прошлый раз, и
// убирает те, через которые Spotify отказывался работать.
//
// Если непригодными оказались все, список возвращается целиком: пусть лучше
// приложение попробует ещё раз, чем скажет «серверов нет» при полном списке.
func sortPreferred(servers []Server, prefer string, bad map[string]bool) []Server {
	var first, rest []Server
	for _, s := range servers {
		if bad[s.String()] {
			continue
		}
		if s.String() == prefer {
			first = append(first, s)
			continue
		}
		rest = append(rest, s)
	}
	out := append(first, rest...)
	if len(out) == 0 {
		return servers
	}
	return out
}

// freePort просит свободный порт у самой системы: занимать заранее выбранный
// номер нельзя — на нём может сидеть что угодно.
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// waitPort ждёт, пока Xray начнёт принимать соединения.
func waitPort(ctx context.Context, port int) error {
	deadline := time.Now().Add(startTimeout)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("посредник не поднялся за %s", startTimeout)
}

// kill гасит процесс наверняка: обход не должен пережить приложение.
func kill(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	cmd.Process.Kill()
}

// killAndReap гасит процесс, за которым никто не следит.
//
// За один перебор подписки мы запускаем и гасим до восьми программ обхода. Без
// Wait Windows держит запись о каждой до конца жизни приложения — а следит
// (и дожидается) только за той, которая в итоге заработала.
func killAndReap(cmd *exec.Cmd) {
	kill(cmd)
	go cmd.Wait()
}

// lastLines оставляет от ругани программы последние строки: в начале там
// приветствие и номер версии, а причина отказа всегда в конце.
func lastLines(text string, n int) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, " | "))
}
