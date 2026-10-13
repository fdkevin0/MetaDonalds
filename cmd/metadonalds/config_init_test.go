package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adrg/xdg"
)

func temporaryConfigDiscovery(t *testing.T) string {
	t.Helper()
	t.Cleanup(xdg.Reload)
	t.Chdir(t.TempDir())
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_CONFIG_DIRS", t.TempDir())
	t.Setenv("METADONALDS_CONFIG", "")
	xdg.Reload()
	return filepath.Join(home, "metadonalds", "config.toml")
}
func TestHelpAndVersionDoNotInitializeConfiguration(t *testing.T) {
	path := temporaryConfigDiscovery(t)
	for _, args := range [][]string{{"--version"}, {"--help"}, {"serve", "--help"}, {"web", "--help"}, {"tui", "--help"}} {
		cmd := newCommand()
		cmd.SetArgs(args)
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("help created configuration")
	}
}
func TestExplicitEnvironmentConfigDoesNotInitialize(t *testing.T) {
	path := temporaryConfigDiscovery(t)
	t.Setenv("METADONALDS_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	cmd := newCommand()
	cmd.SetArgs([]string{"serve"})
	cmd.SetErr(io.Discard)
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("missing environment config accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("explicit environment config initialized default")
	}
}
func TestServeAndWebInitializeConfiguration(t *testing.T) {
	for _, name := range []string{"serve", "web"} {
		t.Run(name, func(t *testing.T) {
			path := temporaryConfigDiscovery(t)
			if name == "web" {
				fakeBrowser(t, 0)
			} else {
				t.Setenv("PATH", t.TempDir())
			}
			// A private absent socket prevents reuse of any real rclone service.
			socketDir, err := os.MkdirTemp("", "md-init-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(socketDir) })
			t.Setenv("METADONALDS_PIKPAK_RC_SOCKET", filepath.Join(socketDir, "rclone.sock"))
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			listener.Close()
			args := []string{name, "--listen", address}
			if name == "web" {
				args = []string{name, "--server", "http://" + address}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := newCommand()
			cmd.SetArgs(args)
			var output bytes.Buffer
			cmd.SetErr(&output)
			cmd.SetOut(&output)
			done := make(chan error, 1)
			go func() { done <- cmd.ExecuteContext(ctx) }()
			ready := false
			client := &http.Client{Timeout: 100 * time.Millisecond}
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				response, err := client.Get("http://" + address + "/healthz")
				if err == nil {
					response.Body.Close()
					if response.StatusCode == 200 {
						ready = true
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if !ready {
				t.Fatalf("%s did not become ready: %s", name, &output)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("%s did not initialize config: %v", name, err)
			}
		})
	}
}
