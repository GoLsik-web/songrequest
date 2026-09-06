//go:build windows

package tunnel

import (
	"os/exec"
	"syscall"
)

// hideWindow прячет консольное окно программы обхода.
//
// Без этого при каждом включении обхода посреди стрима выскакивало бы чёрное
// окно Xray — на весь экран зрителям. То же самое уже сделано для yt-dlp и mpv.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
