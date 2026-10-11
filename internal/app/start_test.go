package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/client"
)

const testHash = "0123456789abcdef0123456789abcdef01234567"

func freeAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func startupConfig(t *testing.T, upstream string) string {
	t.Helper()
	fakeRclone(t)
	dir := shortDir(t)
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, dir)
	}
	path := filepath.Join(dir, "config.toml")
	// Auto-start must use the requested loopback origin, not this wildcard address.
	data := fmt.Sprintf("[server]\nlisten_addr = '0.0.0.0:1'\n[nyaa]\nbase_url = %q\nf = 2\n[pikpak]\nrc_socket = %q\nremote = 'pikpak:My Pack'\n", upstream, filepath.Join(dir, "rclone.sock"))
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEnsureStartsReusesAndStopsOwnedServer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "test" || r.URL.Query().Get("f") != "2" {
			t.Errorf("config or query lost: %s", r.URL)
		}
		_, _ = w.Write([]byte(`<rss><channel><item><title>test</title><infoHash>` + testHash + `</infoHash><seeders>5</seeders></item></channel></rss>`))
	}))
	defer upstream.Close()
	configPath := startupConfig(t, upstream.URL)
	url := "http://" + freeAddress(t)
	stop, err := Ensure(context.Background(), url, configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	c, _ := client.New(url)
	items, err := c.Search(context.Background(), "test", "", "")
	if err != nil || len(items) != 1 || items[0].InfoHash != testHash {
		t.Fatalf("auto-started API: %v, %v", items, err)
	}
	// A reused server must not require a local config, or be stopped by this UI.
	release, err := Ensure(context.Background(), url, "missing-config.toml")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := health(context.Background(), url); err != nil {
		t.Fatalf("reused server was stopped: %v", err)
	}
	stop()
	if err := health(context.Background(), url); err == nil {
		t.Fatal("owned server still running after cleanup")
	}
}

func TestEnsureConcurrentClientsShareOneServer(t *testing.T) {
	configPath := startupConfig(t, "http://127.0.0.1:1")
	url := "http://" + freeAddress(t)
	var stops [3]func()
	var errs [3]error
	var wg sync.WaitGroup
	for i := range stops {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stops[i], errs[i] = Ensure(context.Background(), url, configPath)
		}()
	}
	wg.Wait()
	for _, stop := range stops {
		if stop != nil {
			defer stop()
		}
	}
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := health(context.Background(), url); err != nil {
		t.Fatal(err)
	}
	logs, err := filepath.Glob(filepath.Join(filepath.Dir(configPath), "metadonalds-server-*.log"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("expected one owned server, log files=%v, err=%v", logs, err)
	}
}

func TestEnsureStartupFailuresLeavePortAvailable(t *testing.T) {
	addr := freeAddress(t)
	url := "http://" + addr
	_, err := Ensure(context.Background(), url, filepath.Join(t.TempDir(), "missing.toml"))
	if err == nil || !strings.Contains(err.Error(), "-config") {
		t.Fatalf("missing configuration: %v", err)
	}
	if _, err = Ensure(context.Background(), "https://"+addr, "missing.toml"); err == nil || !strings.Contains(err.Error(), "local HTTP") {
		t.Fatalf("must not start plaintext server for HTTPS: %v", err)
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed startup leaked listener: %v", err)
	}
	listener.Close()
}

func TestEnsureDoesNotReplaceOccupiedPort(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("unrelated service")) }))
	defer other.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, err := Ensure(ctx, other.URL, "missing.toml")
	if err == nil || !strings.Contains(err.Error(), "port occupied") {
		t.Fatalf("occupied port: %v", err)
	}
	resp, err := http.Get(other.URL)
	if err != nil {
		t.Fatalf("existing service was interrupted: %v", err)
	}
	resp.Body.Close()
}

func TestExampleConfigStartsWithoutSystemDirectories(t *testing.T) {
	fakeRclone(t)
	dir := shortDir(t)
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, dir)
	}
	url := "http://" + freeAddress(t)
	stop, err := Ensure(context.Background(), url, "../../config.example.toml")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	cfg, err := LoadConfig("../../config.example.toml", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.PikPak.RCSocket, dir+string(os.PathSeparator)) {
		t.Fatalf("default socket is outside the user temporary directory: %s", cfg.PikPak.RCSocket)
	}
}
