//go:build !linux && !darwin && !windows

package web

import "os/exec"

func detachBrowser(*exec.Cmd) {}
