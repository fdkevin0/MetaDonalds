package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adrg/xdg"
	"github.com/fdkevin0/MetaDonalds/internal/core"
)

func temporaryConfigDiscovery(t *testing.T) string {
	t.Helper()
	t.Setenv("METADONALDS_DAV_CACHE_DIR", t.TempDir())
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
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{{"--version"}, {"--help"}, {"serve", "--help"}, {"serve", "--open", "--help"}, {"tui", "--help"}} {
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
func TestServeInitializesConfiguration(t *testing.T) {
	for _, name := range []string{"serve", "serve-open"} {
		t.Run(name, func(t *testing.T) {
			path := temporaryConfigDiscovery(t)
			capture := fakeBrowser(t, 0)
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
			args := []string{"serve", "--listen", address}
			if name == "serve-open" {
				args = append(args, "--open")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := newCommand()
			cmd.SetArgs(args)
			var output lockedOutput
			cmd.SetErr(&output)
			cmd.SetOut(&output)
			done := make(chan error, 1)
			go func() { done <- cmd.ExecuteContext(ctx) }()
			ready := false
			transport := &http.Transport{DisableKeepAlives: true}
			client := &http.Client{Timeout: 100 * time.Millisecond, Transport: transport}
			defer transport.CloseIdleConnections()
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				response, err := client.Get("http://" + address + "/healthz")
				if err == nil {
					_, _ = io.Copy(io.Discard, response.Body)
					response.Body.Close()
					if response.StatusCode == 200 {
						ready = true
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if ready {
				if name == "serve-open" {
					waitOpened(t, capture)
				}
				response, err := client.Get("http://" + address + "/api/v1/plugins")
				if err != nil {
					t.Fatal(err)
				}
				var catalog struct {
					Plugins []core.Plugin `json:"plugins"`
				}
				err = json.NewDecoder(response.Body).Decode(&catalog)
				_, _ = io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				ids := map[string]string{}
				for _, plugin := range catalog.Plugins {
					ids[plugin.ID] = plugin.Type
				}
				if len(ids) != 3 || ids["nyaa"] != "nyaa" || ids["catalog"] != "javdb" || ids["pikpak"] != "pikpak-rclone" {
					t.Errorf("first-run plugins: %+v", catalog.Plugins)
				}
			}
			transport.CloseIdleConnections()
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if !ready {
				t.Fatalf("%s did not become ready: %s", name, &output)
			}
			if name == "serve" {
				if _, err := os.Stat(capture); !os.IsNotExist(err) {
					t.Fatal("serve without --open launched browser")
				}
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("%s did not initialize config: %v", name, err)
			}
		})
	}
}
