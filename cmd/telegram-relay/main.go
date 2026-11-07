// Обработчик общего бота запускается только на компьютере автора.
package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"songrequest/internal/secrets"
	tg "songrequest/internal/telegram"
)

func main() {
	secrets.SetService("songrequest-common-telegram")
	store := secrets.New()
	if len(os.Args) > 1 && (os.Args[1] == "--set-token" || os.Args[1] == "--set-tunnel-token") {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 4097))
		token := strings.TrimSpace(string(data))
		name := "bot_token"
		valid := tg.ValidToken(token)
		if os.Args[1] == "--set-tunnel-token" {
			name = "tunnel_token"
			valid = len(token) >= 64 && len(token) <= 4096 && !strings.ContainsAny(token, "\r\n\x00")
		}
		if err != nil || !valid {
			os.Exit(2)
		}
		if store.PutJSON(name, token) != nil {
			os.Exit(3)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--run-tunnel" {
		runTunnel(store, os.Args[2])
		return
	}
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "Usage: telegram-relay.exe <relay.py>")
		os.Exit(2)
	}
	script, err := filepath.Abs(os.Args[1])
	if err != nil {
		os.Exit(2)
	}
	runtimeDir := filepath.Join(filepath.Dir(script), "runtime")
	if os.MkdirAll(runtimeDir, 0700) != nil {
		os.Exit(3)
	}
	log, err := os.OpenFile(filepath.Join(runtimeDir, "relay.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(3)
	}
	defer log.Close()
	for {
		if connection, err := net.DialTimeout("tcp", "127.0.0.1:8980", time.Second); err == nil {
			connection.Close()
			fmt.Fprintln(log, "Relay port already in use; no second poller started")
			return
		}
		var token string
		if store.GetJSON("bot_token", &token) != nil || token == "" {
			fmt.Fprintln(log, "Token is not configured; run configure.py")
			return
		}
		python := "python"
		if len(os.Args) == 3 {
			python = os.Args[2]
		}
		command := exec.Command(python, script)
		hideWindow(command)
		command.Dir = filepath.Dir(script)
		command.Env = append(os.Environ(), "SONGREQUEST_COMMON_BOT_TOKEN="+token)
		command.Stdout = log
		command.Stderr = log
		if err := command.Run(); err != nil {
			fmt.Fprintln(log, "Relay stopped; retrying in 15 seconds")
		}
		time.Sleep(15 * time.Second)
	}
}
