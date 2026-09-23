//go:build !windows

package selfupdate

import (
	"os/exec"
	"syscall"
)

func spawnDetached(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
