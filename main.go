// Команда songrequest — заказ музыки зрителями Twitch с проигрыванием в Spotify
// самого стримера. Приложение локальное: наружу ничего не слушает.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"songrequest/internal/app"
	"songrequest/internal/config"
	"songrequest/internal/server"
	"songrequest/internal/store"
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
	dir, err := dataDir()
	if err != nil {
		return err
	}

	log, closeLog, err := newLogger(dir)
	if err != nil {
		return err
	}
	defer closeLog()

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

	srv, err := server.New(state, cfg, log)
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
	openBrowser(srv.Addr(), log)

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

// newLogger пишет и в консоль, и в файл: консоль стример видит сразу, а файл
// пригодится, когда он придёт с вопросом «оно сломалось».
func newLogger(dir string) (*slog.Logger, func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, "songrequest.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("не смог открыть файл лога: %w", err)
	}

	h := slog.NewTextHandler(io.MultiWriter(os.Stderr, f), &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(h), func() { f.Close() }, nil
}

// openBrowser открывает панель. Не смогли — не беда, адрес напечатан выше.
func openBrowser(url string, log *slog.Logger) {
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
