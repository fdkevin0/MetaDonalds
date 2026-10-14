package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func fakeBrowser(t *testing.T, exitCode int) string {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("POSIX launcher fixture")
	}
	dir := t.TempDir()
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	capture := filepath.Join(dir, "opened")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' \"$1\" > \"$BROWSER_TEST_CAPTURE\"\nexit %d\n", exitCode)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("BROWSER_TEST_CAPTURE", capture)
	return capture
}
func waitOpened(t *testing.T, capture string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(capture); err == nil && len(data) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("browser launcher did not run")
}
func assertHealthy(t *testing.T, address string) {
	t.Helper()
	c := &http.Client{Timeout: time.Second}
	response, err := c.Get(address + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || string(body) != "ok\n" {
		t.Fatalf("health: %d %q", response.StatusCode, body)
	}
}

type lockedOutput struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}
func (b *lockedOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}
func emptyConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[server]\nlisten_addr='127.0.0.1:0'\n[plugins]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestServeOpenSurvivesBrowserFailure(t *testing.T) {
	for _, mode := range []string{"exit-error", "start-error"} {
		t.Run(mode, func(t *testing.T) {
			capture := fakeBrowser(t, 7)
			if mode == "start-error" {
				t.Setenv("PATH", t.TempDir())
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := newCommand()
			cmd.SetArgs([]string{"serve", "--open", "--listen", "127.0.0.1:0", "--config", emptyConfig(t)})
			var output lockedOutput
			cmd.SetErr(&output)
			done := make(chan error, 1)
			go func() { done <- cmd.ExecuteContext(ctx) }()
			deadline := time.Now().Add(5 * time.Second)
			for !strings.Contains(output.String(), "manually") && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if !strings.Contains(output.String(), "manually") {
				t.Fatalf("missing manual fallback: %s", output.String())
			}
			address := strings.TrimSuffix(strings.Fields(output.String())[1], "/")
			if mode == "exit-error" {
				waitOpened(t, capture)
				data, _ := os.ReadFile(capture)
				if string(data) != address+"/" {
					t.Fatalf("opened %q, reported %s", data, address)
				}
			}
			assertHealthy(t, address)
			select {
			case err := <-done:
				t.Fatalf("browser failure stopped command: %v", err)
			default:
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestServeOpenDoesNotReuseOccupiedPort(t *testing.T) {
	capture := fakeBrowser(t, 0)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })}
	go server.Serve(listener)
	defer server.Close()
	cmd := newCommand()
	cmd.SetArgs([]string{"serve", "--open", "--listen", listener.Addr().String(), "--config", emptyConfig(t)})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("occupied port reused")
	}
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatal("browser opened after bind failure")
	}
	assertHealthy(t, "http://"+listener.Addr().String())
}
func TestServeOpenSignalStopsServer(t *testing.T) {
	if os.Getenv("METADONALDS_SERVE_HELPER") == "1" {
		os.Args = []string{"metadonalds", "serve", "--open", "--listen", os.Getenv("SERVE_HELPER_LISTEN"), "--config", os.Getenv("SERVE_HELPER_CONFIG")}
		main()
		os.Exit(0)
	}
	capture := fakeBrowser(t, 0)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := "http://" + listener.Addr().String()
	listener.Close()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err = os.WriteFile(path, []byte("[server]\nlisten_addr='127.0.0.1:8080'\n[plugins]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	process := exec.Command(executable, "-test.run=^TestServeOpenSignalStopsServer$")
	process.Env = append(os.Environ(), "METADONALDS_SERVE_HELPER=1", "SERVE_HELPER_LISTEN="+strings.TrimPrefix(address, "http://"), "SERVE_HELPER_CONFIG="+path)
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
	assertHealthy(t, address)
	if err = process.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	select {
	case err = <-done:
		if err != nil {
			t.Fatalf("signal shutdown: %v %s", err, output.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM did not stop command")
	}
	c := &http.Client{Timeout: time.Second}
	if response, err := c.Get(address + "/healthz"); err == nil {
		response.Body.Close()
		t.Fatal("owned server survived exit")
	}
}
