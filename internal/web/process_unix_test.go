//go:build linux || darwin

package web

import (
	"os/exec"
	"testing"
)

func TestBrowserHasIndependentSignalGroup(t *testing.T) {
	cmd := exec.Command("unused")
	detachBrowser(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("browser would inherit CLI foreground signal group")
	}
}
