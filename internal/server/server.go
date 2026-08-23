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
	"sync"
	"time"

	"songrequest/internal/app"
	"songrequest/internal/config"
	"songrequest/internal/logx"
	"songrequest/internal/spotify"
	"songrequest/internal/store"
	"songrequest/internal/twitch"
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
	ctx           context.Context
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
	mux.HandleFunc("POST /api/log/debug", s.handleDebugLog)
	mux.HandleFunc("GET /api/diag/export", s.handleDiagExport)

	mux.HandleFunc("POST /api/spotify/login", s.handleSpotifyLogin)
	mux.HandleFunc("POST /api/spotify/logout", s.handleSpotifyLogout)
	mux.HandleFunc("POST /api/spotify/check", s.handleSpotifyCheck)
	mux.HandleFunc("GET /api/spotify/playlists", s.handleSpotifyPlaylists)
	mux.HandleFunc("POST /api/spotify/snapshot", s.handleSpotifySnapshot)
	mux.HandleFunc("POST /api/spotify/restore", s.handleSpotifyRestore)
	mux.HandleFunc("GET /callback", s.handleSpotifyCallback)

	mux.HandleFunc("POST /api/twitch/login", s.handleTwitchLogin)
	mux.HandleFunc("POST /api/twitch/logout", s.handleTwitchLogout)
	mux.HandleFunc("POST /api/redemptions/{id}", s.handleRedemptionAction)

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
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "[::1]" && host != "::1" {
			http.Error(w, "запрос не с этого компьютера", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
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
	var incoming config.Config
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&incoming); err != nil {
		http.Error(w, "не разобрал настройки", http.StatusBadRequest)
		return
	}

	// Порт меняется только при перезапуске, поэтому текущий сохраняем как есть.
	current := s.cfg.Get()
	incoming.Port = current.Port

	if err := s.cfg.Update(func(c *config.Config) { *c = incoming }); err != nil {
		s.log.Error("сохранение настроек", "ошибка", err)
		http.Error(w, "не смог сохранить настройки", http.StatusInternalServerError)
		return
	}
	s.syncSpotifyInfo()
	s.state.Notify("info", "Настройки сохранены")
	writeJSON(w, s.cfg.Get())
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
