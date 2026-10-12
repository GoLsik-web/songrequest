//go:build !windows

package procjob

import "os/exec"

// Bind — на других системах ничего не делает.
func Bind(cmd *exec.Cmd) {}
