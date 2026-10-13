//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWebTerminalInterruptDoesNotKillBrowser(t *testing.T) {
	if os.Getenv("METADONALDS_WEB_GROUP_HELPER") == "1" {
		os.Args = []string{"metadonalds", "web", "--server", os.Getenv("WEB_HELPER_URL"), "--config", os.Getenv("WEB_HELPER_CONFIG")}
		main()
		os.Exit(0)
	}
	dir := t.TempDir()
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	capture := filepath.Join(dir, "browser-pid")
	// Stay alive like a launcher that execs its browser; parent shutdown must not signal it.
	script := "#!/bin/sh\ntrap 'exit 99' INT\nprintf '%s' \"$$\" > \"$BROWSER_TEST_CAPTURE\"\nwhile :; do /bin/sleep 0.05; done\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("BROWSER_TEST_CAPTURE", capture)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := "http://" + listener.Addr().String()
	listener.Close()
	path := filepath.Join(dir, "config.toml")
	if err = os.WriteFile(path, []byte("[server]\nlisten_addr='127.0.0.1:8080'\n[plugins]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process := exec.CommandContext(ctx, executable, "-test.run=^TestWebTerminalInterruptDoesNotKillBrowser$")
	process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	process.Env = append(os.Environ(), "METADONALDS_WEB_GROUP_HELPER=1", "WEB_HELPER_URL="+address, "WEB_HELPER_CONFIG="+path)
	var output bytes.Buffer
	process.Stdout = &output
	process.Stderr = &output
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if process.ProcessState == nil {
			process.Process.Kill()
			process.Wait()
		}
	})
	waitOpened(t, capture)
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	browserPID, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(-browserPID, syscall.SIGKILL) })
	assertHealthy(t, address)
	browserGroup, err := syscall.Getpgid(browserPID)
	if err != nil {
		t.Fatal(err)
	}
	if browserGroup == process.Process.Pid {
		t.Fatal("browser shares command signal group")
	}
	if err = syscall.Kill(-process.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	select {
	case err = <-done:
		if err != nil {
			t.Fatalf("group SIGINT: %v %s", err, output.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("terminal interrupt did not stop command")
	}
	if err = syscall.Kill(browserPID, 0); err != nil {
		t.Fatalf("terminal interrupt killed the browser: %v", err)
	}
}
