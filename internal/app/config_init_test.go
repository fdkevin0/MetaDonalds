package app

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/adrg/xdg"
)

func isolatedConfigHome(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv("METADONALDS_DAV_CACHE_DIR", t.TempDir())
	t.Cleanup(xdg.Reload)
	t.Chdir(t.TempDir())
	home := filepath.Join(t.TempDir(), "config-home")
	system := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_CONFIG_DIRS", system)
	xdg.Reload()
	return filepath.Join(home, "metadonalds", "config.toml"), system
}
func captureConfigLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	return &output
}
func TestInitializeMissingConfiguration(t *testing.T) {
	path, _ := isolatedConfigHome(t)
	output := captureConfigLog(t)
	cfg, err := LoadConfig("", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.ListenAddr != "127.0.0.1:8080" || cfg.Nyaa.BaseURL != "https://sukebei.nyaa.si/" || cfg.PikPak.Remote != "pikpak:My Pack" || cfg.Plugins != nil {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	assertDefaultInstances(t, cfg)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != defaultConfig {
		t.Fatalf("default file: %q %v", data, err)
	}
	if runtime.GOOS != "windows" {
		for target, mode := range map[string]os.FileMode{path: 0600, filepath.Dir(path): 0700} {
			info, err := os.Stat(target)
			if err != nil || info.Mode().Perm() != mode {
				t.Fatalf("permissions %s: %v %v", target, info, err)
			}
		}
	}
	if !strings.Contains(output.String(), "initialized default configuration") || !strings.Contains(output.String(), path) {
		t.Fatalf("initialization message: %s", output)
	}
	changed := strings.Replace(defaultConfig, "pikpak:My Pack", "custom:Folder", 1)
	if err = os.WriteFile(path, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig("", "")
	if err != nil || cfg.PikPak.Remote != "custom:Folder" {
		t.Fatalf("existing config overwritten: %v %v", cfg, err)
	}
	assertDefaultInstances(t, cfg)
	if data, err := os.ReadFile(path); err != nil || string(data) != changed {
		t.Fatalf("existing file changed: %q %v", data, err)
	}
	if strings.Count(output.String(), "initialized default configuration") != 1 {
		t.Fatal("initialization reported twice")
	}
	if _, err = os.Stat("config.toml"); !os.IsNotExist(err) {
		t.Fatal("created config in cwd")
	}
}
func TestConcurrentConfigurationInitialization(t *testing.T) {
	path, _ := isolatedConfigHome(t)
	output := captureConfigLog(t)
	const count = 16
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := LoadConfig("", ""); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent reader saw incomplete config: %v", err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != defaultConfig {
		t.Fatalf("published config: %q %v", data, err)
	}
	if strings.Count(output.String(), "initialized default configuration") != 1 {
		t.Fatalf("expected one publisher: %s", output)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files retained: %v %v", entries, err)
	}
}
func TestExistingConfigErrorsNeverInitialize(t *testing.T) {
	for _, kind := range []string{"invalid", "directory", "dangling-symlink", "permission"} {
		t.Run(kind, func(t *testing.T) {
			path, _ := isolatedConfigHome(t)
			captureConfigLog(t)
			switch kind {
			case "invalid":
				if err := os.WriteFile("config.toml", []byte("[bad TOML"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir("config.toml", 0700); err != nil {
					t.Fatal(err)
				}
			case "dangling-symlink":
				if err := os.Symlink("missing-target", "config.toml"); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			case "permission":
				if os.Geteuid() == 0 || runtime.GOOS == "windows" {
					t.Skip("requires Unix permission enforcement")
				}
				if err := os.WriteFile("config.toml", []byte(defaultConfig), 0000); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod("config.toml", 0600)
			}
			if _, err := LoadConfig("", ""); err == nil {
				t.Fatal("invalid existing entry ignored")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("initialized despite an existing config error")
			}
		})
	}
}
func TestInaccessibleXDGDirectoryDoesNotFallBack(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("requires Unix permission enforcement")
	}
	path, system := isolatedConfigHome(t)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Dir(path), 0700)
	systemPath := filepath.Join(system, "metadonalds", "config.toml")
	os.MkdirAll(filepath.Dir(systemPath), 0700)
	os.WriteFile(systemPath, []byte(defaultConfig), 0600)
	if _, err := LoadConfig("", ""); err == nil || !strings.Contains(err.Error(), "inspect configuration") {
		t.Fatalf("inaccessible home skipped: %v", err)
	}
}
func TestExplicitMissingConfigurationDoesNotInitialize(t *testing.T) {
	path, _ := isolatedConfigHome(t)
	output := captureConfigLog(t)
	if _, err := LoadConfig("missing.toml", ""); err == nil {
		t.Fatal("explicit missing config accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("explicit missing path initialized default")
	}
	if output.Len() != 0 {
		t.Fatalf("unexpected log: %s", output)
	}
}
func TestEnsureHealthyServiceDoesNotInitializeConfiguration(t *testing.T) {
	path, _ := isolatedConfigHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) }))
	defer srv.Close()
	stop, err := Ensure(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("reused service created config")
	}
}
func TestEnsureInitializesBeforeStartingLocalService(t *testing.T) {
	path, _ := isolatedConfigHome(t)
	output := captureConfigLog(t)
	// No real rclone or network source: only local service readiness is exercised.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("METADONALDS_PIKPAK_RC_SOCKET", filepath.Join(shortDir(t), "rclone.sock"))
	address := "http://" + freeAddress(t)
	stop, err := Ensure(context.Background(), address, "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if err = health(context.Background(), address); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), path) {
		t.Fatal("startup initialization path not visible")
	}
	stop()
	if err = health(context.Background(), address); err == nil {
		t.Fatal("owned service still running")
	}
}
