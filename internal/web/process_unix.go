//go:build linux || darwin

package web

import (
	"os/exec"
	"syscall"
)

func detachBrowser(cmd *exec.Cmd) {
	// A terminal Ctrl+C targets our foreground group, not the launched browser.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
