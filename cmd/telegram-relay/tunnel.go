package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"songrequest/internal/secrets"
)

func runTunnel(store *secrets.Store, executable string) {
	runtimeDir := filepath.Join(filepath.Dir(executable), "runtime")
	if os.MkdirAll(runtimeDir, 0700) != nil {
		return
	}
	log, err := os.OpenFile(filepath.Join(runtimeDir, "tunnel.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer log.Close()
	for {
		var token string
		if store.GetJSON("tunnel_token", &token) != nil || token == "" {
			fmt.Fprintln(log, "Tunnel token missing; run configure_tunnel.py")
			return
		}
		command := exec.Command(executable, "--no-autoupdate", "tunnel", "--metrics", "127.0.0.1:8981", "run")
		hideWindow(command)
		command.Env = append(os.Environ(), "TUNNEL_TOKEN="+token)
		command.Stdout = log
		command.Stderr = log
		if err := command.Run(); err != nil {
			fmt.Fprintln(log, "Tunnel stopped; retrying in 15 seconds")
		}
		time.Sleep(15 * time.Second)
	}
}
