// Package server — локальная панель управления и виджет для OBS.
//
// Слушаем строго 127.0.0.1: наружу порт не открывается никогда. Статика зашита
// в бинарник через go:embed, поэтому распространяется один .exe без папок рядом.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"songrequest/internal/app"
	"songrequest/internal/config"
	"songrequest/internal/donations"
	"songrequest/internal/links"
	"songrequest/internal/logx"
	"songrequest/internal/match"
	"songrequest/internal/player"
	"songrequest/internal/queue"
	"songrequest/internal/spotify"
	"songrequest/internal/store"
	"songrequest/internal/twitch"
	"songrequest/internal/youtube"
)

//go:embed all:web
var webFS embed.FS

// Deps — всё, что панели нужно для работы. Отдельной структурой, чтобы
// добавление Twitch и донатов не переписывало сигнатуру каждый раз.
type Deps struct {
	State   *app.State
	Cfg     *config.File
	Log     *logx.Logger
	Spotify *spotify.Client
	Twitch  *twitch.Client
	DB      *store.DB
	DataDir string
	Version string
}

// Server — HTTP-сервер панели.
type Server struct {
	state   *app.State
	cfg     *config.File
	log     *logx.Logger
	spotify *spotify.Client
	twitch  *twitch.Client
	db      *store.DB
	// matchCache помнит, чем закончился поиск по такому же запросу.
	matchCache *match.Cache
	queue      *queue.Queue
	player     *player.Player

	donations      *donations.Hub
	donationAlerts *donations.DonationAlerts

	ytTools *youtube.Tools
	youtube *youtube.Player
	yandex  *links.YandexReader
	dataDir string
	version string

	http *http.Server
	ln   net.Listener
	addr string

	// snapMu защищает снимок состояния Spotify. На этом этапе его снимают
	// кнопкой из панели; дальше это будет делать очередь заказов.
	snapMu sync.Mutex
	snap   *spotify.Snapshot

	// mu защищает то, что заполняется по ходу подключения Twitch.
	mu            sync.Mutex
	rewardID      string
	eventsRunning bool
	// eventsReward — награда, на которую подписана живая подписка. Если она
	// разошлась с текущей, подписку надо поднимать заново.
	eventsReward string
	stopEvents   context.CancelFunc
	ctx          context.Context

	// skipActor — кто оборвал текущий заказ. Живёт до конца этого заказа:
	// историю пишет плеер, а имя человека знает только панель.
	skipActor string

	// ownNow — то, что стример слушает сам между заказами. Показывается в
	// виджете, когда очередь пуста.
	ownNow *app.NowPlaying
}

// New поднимает слушатель на 127.0.0.1. Если желаемый порт занят, берём любой
// свободный — приложение не должно отказываться стартовать из-за этого.
func New(d Deps) (*Server, error) {
	state, cfg, log := d.State, d.Cfg, d.Log
	want := cfg.Get().Port

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", want))
	if err != nil && want != 0 {
		log.Warn("порт занят, беру свободный", "порт", want)
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return nil, fmt.Errorf("не удалось занять локальный порт: %w", err)
	}

	s := &Server{
		state:   state,
		cfg:     cfg,
		log:     log,
		spotify: d.Spotify,
		twitch:  d.Twitch,
		db:      d.DB,
		dataDir: d.DataDir,
		version: d.Version,
		ln:      ln,
		addr:    "http://" + ln.Addr().String(),
	}

	if d.DB != nil {
		s.matchCache = match.NewCache(d.DB.SQL())
		s.queue = queue.New(d.DB.SQL())
		s.setupPlayer(d.Cfg)
	}
	s.setupDonations()
	s.yandex = links.NewYandexReader()

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(sub)))
	mux.HandleFunc("GET /{$}", s.page(sub, "index.html"))
	mux.HandleFunc("GET /widget", s.page(sub, "widget.html"))
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("POST /api/config", s.handleSetConfig)
	mux.HandleFunc("GET /api/diag/export", s.handleDiagExport)

	mux.HandleFunc("POST /api/spotify/login", s.handleSpotifyLogin)
	mux.HandleFunc("POST /api/spotify/logout", s.handleSpotifyLogout)
	mux.HandleFunc("POST /api/spotify/check", s.handleSpotifyCheck)
	mux.HandleFunc("POST /api/spotify/proxy/detect", s.handleProxyDetect)
	mux.HandleFunc("GET /api/spotify/playlists", s.handleSpotifyPlaylists)
	mux.HandleFunc("POST /api/spotify/snapshot", s.handleSpotifySnapshot)
	mux.HandleFunc("POST /api/spotify/restore", s.handleSpotifyRestore)
	mux.HandleFunc("GET /callback", s.handleSpotifyCallback)

	mux.HandleFunc("POST /api/twitch/login", s.handleTwitchLogin)
	mux.HandleFunc("POST /api/twitch/logout", s.handleTwitchLogout)
	mux.HandleFunc("POST /api/redemptions/{id}", s.handleRedemptionAction)

	mux.HandleFunc("POST /api/queue/skip", s.handleSkip)
	mux.HandleFunc("POST /api/queue/{id}/remove", s.handleQueueRemove)
	mux.HandleFunc("POST /api/queue/{id}/top", s.handleQueueTop)
	mux.HandleFunc("POST /api/queue/{id}/fix", s.handleQueueFix)
	mux.HandleFunc("GET /api/search", s.handleSearch)
	mux.HandleFunc("POST /api/queue/reorder", s.handleQueueReorder)
	mux.HandleFunc("POST /api/queue/clear", s.handleQueueClear)
	mux.HandleFunc("POST /api/queue/pause", s.handlePause)
	mux.HandleFunc("POST /api/bans", s.handleBan)
	mux.HandleFunc("GET /api/bans", s.handleBans)
	mux.HandleFunc("GET /api/history", s.handleHistory)
	mux.HandleFunc("GET /api/modlog", s.handleModLog)

	mux.HandleFunc("POST /api/donations/login", s.handleDonationAlertsLogin)
	mux.HandleFunc("POST /api/donations/logout", s.handleDonationAlertsLogout)
	mux.HandleFunc("POST /api/donations/token", s.handleDonationsToken)
	mux.HandleFunc("GET /donations/callback", s.handleDonationAlertsCallback)

	// Прокси включаем до первого запроса: иначе первая же проверка связи
	// пойдёт напрямую и упрётся в блокировку.
	s.applyProxy()

	// Виджет должен получить оформление сразу при подключении, ещё до первой
	// правки настроек.
	s.state.SetWidget(s.cfg.Get().Widget)

	s.http = &http.Server{
		Handler:           s.guard(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s, nil
}

// Addr — адрес панели, его показываем в консоли и открываем в браузере.
func (s *Server) Addr() string { return s.addr }

// baseContext — контекст жизни приложения. Фоновые задачи (ожидание кода,
// подписка на события) должны переживать отдельный HTTP-запрос, но умирать
// вместе с приложением.
func (s *Server) baseContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

// Serve обслуживает запросы до отмены контекста.
func (s *Server) Serve(ctx context.Context) error {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.http.Shutdown(shutCtx)
	}()

	err := s.http.Serve(s.ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// guard закрывает две дыры разом: запросы с чужим Host (DNS rebinding, когда
// сайт в браузере резолвит свой домен в 127.0.0.1 и стучится к нам) и кэш
// страниц панели, из-за которого после обновления показывалась бы старая версия.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r.Host) {
			http.Error(w, "запрос не с этого компьютера", http.StatusForbidden)
			return
		}

		// Origin у любого запроса, изменяющего состояние, обязан быть нашим.
		//
		// Проверки одного Host мало. Панель открыта в обычном браузере
		// стримера, а браузер честно исполняет запросы, которые ему велела
		// сделать любая другая вкладка. Сайт, открытый в соседней вкладке,
		// мог отправить нам форму или fetch — Host в таком запросе всё равно
		// наш, — и очистить очередь, разлогинить Spotify или, что хуже
		// всего, записать в настройки свой адрес прокси: после этого все
		// запросы к Spotify вместе с ключом доступа пошли бы через чужой
		// сервер.
		//
		// Браузеры на межсайтовый POST заголовок Origin шлют всегда, поэтому
		// проверять его достаточно; а на своих запросах он равен нашему
		// адресу. Если Origin нет вовсе (старый браузер, curl, наш же код) —
		// пропускаем: подделать так может только программа на этом же
		// компьютере, а у неё и без нас есть все права.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" && !localOrigin(origin) {
				s.log.Warn("запрос с чужого сайта отклонён", "origin", origin, "путь", r.URL.Path)
				http.Error(w, "запрос с чужого сайта", http.StatusForbidden)
				return
			}
		}

		w.Header().Set("Cache-Control", "no-store")
		// Чтобы панель нельзя было спрятать в прозрачном кадре на чужом сайте
		// и подловить нажатие стримера.
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		next.ServeHTTP(w, r)
	})
}

// localHost сообщает, обращаются ли к нам по нашему собственному адресу.
func localHost(hostPort string) bool {
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = hostPort
	}
	return host == "127.0.0.1" || host == "localhost" || host == "[::1]" || host == "::1"
}

// localOrigin разбирает заголовок Origin и говорит, наш ли это адрес.
func localOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return localHost(u.Host)
}

func (s *Server) page(sub fs.FS, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(sub, name)
		if err != nil {
			http.Error(w, "страница не найдена", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	}
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.state.Snapshot())
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.cfg.Get())
}

func (s *Server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	// Начинаем с текущих настроек, а не с пустой структуры.
	//
	// Раньше сюда разбирался нулевой config.Config, и всё, чего не оказалось
	// в теле запроса, молча становилось нулём и уезжало на диск. Панель
	// показывает не все поля: пороги подбора и веса оценок она не знает
	// вовсе. Достаточно было один раз сохранить настройки из старой вкладки
	// (или из чужого скрипта), и match_accept с match_maybe обнулялись —
	// после чего текстовый заказ либо не находил ничего никогда, либо
	// принимал первый попавшийся трек. Починить это из панели было нельзя:
	// полей в ней нет, а переживало оно перезапуск.
	current := s.cfg.Get()
	incoming := current
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&incoming); err != nil {
		http.Error(w, "не разобрал настройки", http.StatusBadRequest)
		return
	}

	// Порт меняется только при перезапуске, поэтому текущий сохраняем как есть.
	incoming.Port = current.Port
	// А пороги подбора чиним, даже если их прислали испорченными: заказ по
	// тексту не должен зависеть от того, что кто-то записал в config.json.
	incoming.NormalizeMatching()

	// Вкладка могла быть открыта до того, как приложение само нашло прокси.
	// Тогда она пришлёт пустое поле и затрёт находку. Пустое значение здесь
	// означает «я про это не знаю», а не «убери».
	if strings.TrimSpace(incoming.SpotifyProxy) == "" && current.SpotifyProxy != "" &&
		!r.URL.Query().Has("proxy") {
		incoming.SpotifyProxy = current.SpotifyProxy
	}
	incoming.Widget.Normalize()

	// Прокси мог измениться — переключаем сразу, не дожидаясь перезапуска.
	proxyChanged := incoming.SpotifyProxy != current.SpotifyProxy
	// Название и цена награды живут не только в конфиге, но и на самом канале.
	// Раньше они доезжали туда лишь при следующем запуске: стример менял цену,
	// на канале оставалась старая, и выглядело это как «настройки не работают».
	rewardChanged := incoming.RewardTitle != current.RewardTitle ||
		incoming.RewardCost != current.RewardCost
	// Ключи донат-сервисов: вставил в панели — подключение обязано подняться
	// сейчас, а не после перезапуска приложения.
	donatePayChanged := incoming.DonatePayKey != current.DonatePayKey
	donateXChanged := incoming.DonateXKey != current.DonateXKey

	if err := s.cfg.Update(func(c *config.Config) { *c = incoming }); err != nil {
		s.log.Error("сохранение настроек", "ошибка", err)
		http.Error(w, "не смог сохранить настройки", http.StatusInternalServerError)
		return
	}
	// Обе карточки: сохранили Client ID Twitch — лампочка Twitch обязана
	// перестать говорить «Не настроено» прямо сейчас, иначе это выглядит
	// так, будто сохранение не сработало.
	if proxyChanged {
		s.applyProxy()
	}
	if rewardChanged {
		go s.pushReward()
	}
	if donatePayChanged {
		s.donations.Restart("DonatePay")
	}
	if donateXChanged {
		s.donations.Restart("DonateX")
	}
	// Настройки, которые раньше читались только при запуске: задержка
	// возврата, «дожидаться конца трека», устройство и браузер для YouTube.
	// Подпись в панели обещала, что они применятся к следующему заказу, —
	// теперь это правда.
	s.applyPlayerSettings()
	s.applyYouTubeSettings()
	s.syncSpotifyInfo()
	s.syncTwitchInfo()
	// Оформление виджета едет в OBS через то же состояние: правка в панели
	// видна на сцене сразу, перезагружать источник не нужно.
	s.state.SetWidget(s.cfg.Get().Widget)
	s.state.Notify("info", "Настройки сохранены")
	writeJSON(w, s.cfg.Get())
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
