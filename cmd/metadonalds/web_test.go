package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
func TestWebReusesServiceAndSurvivesBrowserFailure(t *testing.T) {
	capture := fakeBrowser(t, 7)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := newCommand()
	cmd.SetArgs([]string{"web", "--server", srv.URL, "--config", "/missing/config.toml"})
	var output bytes.Buffer
	cmd.SetErr(&output)
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	waitOpened(t, capture)
	select {
	case err := <-done:
		t.Fatalf("browser failure stopped command: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	assertHealthy(t, srv.URL)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertHealthy(t, srv.URL)
	if !strings.Contains(output.String(), srv.URL) || !strings.Contains(output.String(), "manually") {
		t.Fatalf("missing manual fallback: %s", output.String())
	}
}
func TestWebSignalStopsOnlyOwnedServer(t *testing.T) {
	if os.Getenv("METADONALDS_WEB_HELPER") == "1" {
		os.Args = []string{"metadonalds", "web", "--server", os.Getenv("WEB_HELPER_URL"), "--config", os.Getenv("WEB_HELPER_CONFIG")}
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
	process := exec.Command(executable, "-test.run=^TestWebSignalStopsOnlyOwnedServer$")
	process.Env = append(os.Environ(), "METADONALDS_WEB_HELPER=1", "WEB_HELPER_URL="+address, "WEB_HELPER_CONFIG="+path)
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
