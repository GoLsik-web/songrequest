// Команда songrequest — заказ музыки зрителями Twitch с проигрыванием в Spotify
// самого стримера. Приложение локальное: наружу ничего не слушает.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"songrequest/internal/app"
	"songrequest/internal/config"
	"songrequest/internal/logx"
	"songrequest/internal/secrets"
	"songrequest/internal/server"
	"songrequest/internal/spotify"
	"songrequest/internal/store"
	"songrequest/internal/twitch"
)

// version подставляется при сборке релиза через -ldflags.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		fmt.Fprintln(os.Stderr, "\nНажми Enter, чтобы закрыть окно.")
		fmt.Fscanln(os.Stdin)
		os.Exit(1)
	}
}

func run() error {
	noBrowser := flag.Bool("без-браузера", false, "не открывать панель автоматически")
	flag.Parse()

	dir, err := dataDir()
	if err != nil {
		return err
	}

	// Подробный лог включён всегда. Приложение чинится по логу с чужого
	// компьютера, а просить человека «включи галочку и повтори» — значит
	// потерять тот единственный раз, когда всё сломалось.
	log, err := logx.New(dir, true)
	if err != nil {
		return err
	}
	defer log.Close()

	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}

	db, err := store.Open(dir)
	if err != nil {
		return err
	}
	defer db.Close()

	state := app.New(version)

	keys := secrets.New()
	sp := spotify.New(cfg, log, keys)
	tw := twitch.New(cfg, log, keys)

	srv, err := server.New(server.Deps{
		State:   state,
		Cfg:     cfg,
		Log:     log,
		Spotify: sp,
		Twitch:  tw,
		DB:      db,
		DataDir: dir,
		Version: version,
	})
	if err != nil {
		return err
	}

	// Ctrl+C и закрытие окна должны гасить приложение чисто: закрыть базу,
	// а позже — убить mpv и снять паузу со Spotify.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("приложение запущено", "версия", version, "данные", dir, "панель", srv.Addr())
	fmt.Printf("\n  Панель управления: %s\n  Виджет для OBS:    %s/widget\n  Настройки и база:  %s\n\n  Не закрывай это окно, пока идёт стрим.\n\n",
		srv.Addr(), srv.Addr(), dir)

	state.Notify("info", "Приложение запущено")
	srv.WarnIfPortChanged()
	if !*noBrowser {
		openBrowser(srv.Addr(), log)
	}

	// Если вход был сохранён с прошлого раза, проверяем его сразу: лучше
	// увидеть «Spotify отвалился» до стрима, а не во время первого заказа.
	if sp.Connected() {
		go func() {
			checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if _, err := sp.CheckAccount(checkCtx); err != nil {
				state.NotifyError(err)
			}
			srv.SyncSpotify()
		}()
	} else {
		srv.SyncSpotify()
	}

	// Twitch поднимаем следом: если вход был, приложение само заведёт награду
	// и подпишется на заказы, без единого нажатия.
	srv.SyncTwitch()
	srv.StartTwitchIfConnected(ctx)
	srv.StartPlayer(ctx)
	srv.StartDonations(ctx)
	srv.StartYouTube(ctx)

	// mpv не должен пережить приложение: иначе он останется занимать
	// звуковое устройство, а музыка продолжит играть после закрытия.
	defer srv.StopYouTube()

	return srv.Serve(ctx)
}

// dataDir — папка с настройками, базой и логом: %APPDATA%\songrequest на Windows.
// Держим данные там, а не рядом с .exe, чтобы обновление сводилось к замене файла.
func dataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("не нашёл папку для настроек: %w", err)
	}
	dir := filepath.Join(base, "songrequest")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("не смог создать папку %s: %w", dir, err)
	}
	return dir, nil
}

// openBrowser открывает панель. Не смогли — не беда, адрес напечатан выше.
func openBrowser(url string, log *logx.Logger) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Warn("не открыл браузер сам", "адрес", url)
		return
	}
	go cmd.Wait() // не оставляем зомби-процесс
}
