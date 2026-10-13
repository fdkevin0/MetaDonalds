package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
)

func TestConfig(t *testing.T) {
	if _, err := LoadConfig("../../config.plugins.example.toml", ""); err != nil {
		t.Fatalf("plugin example config: %v", err)
	}
	if _, err := LoadConfig("../../config.example.toml", ""); err != nil {
		t.Fatalf("example config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `[server]
listen_addr = "127.0.0.1:8080"
[nyaa]
base_url = "https://mirror.example/nyaa/"
[pikpak]
rc_socket = "/tmp/rclone.sock"
remote = "pikpak:My Pack"
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path, "")
	if err != nil || cfg.Nyaa.BaseURL != "https://mirror.example/nyaa/" || cfg.Nyaa.F != 0 || cfg.Nyaa.C != "0_0" {
		t.Fatalf("config: %#v, %v", cfg, err)
	}
	custom := strings.Replace(content, "[nyaa]", "[nyaa]\nf = 2\nc = \"1_0\"", 1)
	if err := os.WriteFile(path, []byte(custom), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(path, "")
	if err != nil || cfg.Nyaa.F != 2 || cfg.Nyaa.C != "1_0" {
		t.Fatalf("custom Nyaa config: %#v, %v", cfg.Nyaa, err)
	}
	if err := os.WriteFile(path, []byte(content+"unexpected = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path, ""); err == nil {
		t.Fatal("unknown config key accepted")
	}
}

func TestConfigOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := "[server]\nlisten_addr = '127.0.0.1:8080'\n[nyaa]\nbase_url = 'https://nyaa.si/'\n[pikpak]\nrc_socket = '/tmp/rclone.sock'\nremote = 'pikpak:'\n"
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(content)
	t.Setenv("METADONALDS_SERVER_LISTEN_ADDR", "127.0.0.1:9090")
	t.Setenv("METADONALDS_NYAA_F", "2")
	t.Setenv("METADONALDS_NYAA_C", "1_2")
	t.Setenv("METADONALDS_PIKPAK_RC_SOCKET", "/run/rclone/rclone.sock")
	cfg, err := LoadConfig(path, "")
	if err != nil || cfg.Server.ListenAddr != "127.0.0.1:9090" || cfg.Nyaa.F != 2 || cfg.Nyaa.C != "1_2" || cfg.PikPak.RCSocket != "/run/rclone/rclone.sock" {
		t.Fatalf("environment overrides: %#v, %v", cfg, err)
	}
	cfg, err = LoadConfig(path, "127.0.0.1:7070")
	if err != nil || cfg.Server.ListenAddr != "127.0.0.1:7070" {
		t.Fatalf("flag override: %#v, %v", cfg, err)
	}
	write(strings.Replace(content, "[nyaa]", "[nyaa]\nf = '2'", 1))
	if _, err := LoadConfig(path, ""); err == nil {
		t.Fatal("string TOML filter accepted despite strict types")
	}
	write(content)
	t.Setenv("METADONALDS_NYAA_F", "invalid")
	if _, err := LoadConfig(path, ""); err == nil {
		t.Fatal("invalid integer environment accepted")
	}
	t.Setenv("METADONALDS_NYAA_F", "0")
	t.Setenv("METADONALDS_NYAA_BASE_URL", "")
	if _, err := LoadConfig(path, ""); err == nil {
		t.Fatal("empty required environment accepted")
	}
}

func TestConfigDiscovery(t *testing.T) {
	// Reload after Setenv cleanups as well, so other tests see the original XDG paths.
	t.Cleanup(xdg.Reload)
	t.Chdir(t.TempDir())
	home, system, second := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_CONFIG_DIRS", system+string(os.PathListSeparator)+second)
	xdg.Reload()
	write := func(path, remote string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		data := "[server]\nlisten_addr = '127.0.0.1:8080'\n[nyaa]\nbase_url = 'https://nyaa.si/'\n[pikpak]\nrc_socket = '/tmp/rclone.sock'\nremote = '" + remote + ":'\n"
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(path, remote string) {
		t.Helper()
		cfg, err := LoadConfig(path, "")
		if err != nil || cfg.PikPak.Remote != remote+":" {
			t.Fatalf("config %q = %#v, %v", path, cfg, err)
		}
	}
	write(filepath.Join(second, "metadonalds", "config.toml"), "second")
	check("", "second")
	if _, err := os.Stat(filepath.Join(home, "metadonalds", "config.toml")); !os.IsNotExist(err) {
		t.Fatal("system configuration should prevent user initialization")
	}
	write(filepath.Join(system, "metadonalds", "config.toml"), "system")
	check("", "system")
	write(filepath.Join(home, "metadonalds", "config.toml"), "home")
	check("", "home")
	write("config.toml", "local")
	check("", "local")
	explicit := filepath.Join(t.TempDir(), "explicit.toml")
	write(explicit, "explicit")
	check(explicit, "explicit")
	if _, err := LoadConfig("missing.toml", ""); err == nil {
		t.Fatal("explicit missing file fell back to discovery")
	}
	if err := os.WriteFile("config.toml", []byte("invalid TOML ["), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig("", ""); err == nil {
		t.Fatal("invalid local file fell back to XDG")
	}
}

func TestExplicitPluginsDisableLegacyAndValidateInstances(t *testing.T) {
	legacy := "[server]\nlisten_addr='127.0.0.1:8080'\n[nyaa]\nbase_url='https://nyaa.si/'\n[pikpak]\nremote='pikpak:'\n"
	for _, table := range []string{"[plugins]\n", "[plugins]\nsources=[]\ndownloaders=[]\n"} {
		path := filepath.Join(t.TempDir(), "config.toml")
		os.WriteFile(path, []byte(legacy+table), 0600)
		cfg, err := LoadConfig(path, "")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Plugins == nil || len(cfg.Instances().Sources) != 0 || len(cfg.RCSockets()) != 0 {
			t.Fatalf("explicit plugins fell back to legacy: %+v", cfg)
		}
	}
	header := "[server]\nlisten_addr='127.0.0.1:8080'\n[plugins]\n"
	for _, tc := range []struct {
		name, body string
		bad        bool
	}{
		{"search-only", "[[plugins.sources]]\nid='catalog'\ntype='javdb'\n", false},
		{"multiple-shared-socket", "[[plugins.downloaders]]\nid='one'\ntype='pikpak-rclone'\nrc_socket='/tmp/rclone.sock'\nremote='pikpak:a'\n[[plugins.downloaders]]\nid='two'\ntype='pikpak-rclone'\nrc_socket='/tmp/rclone.sock'\nremote='pikpak:b'\n", false},
		{"duplicate", "[[plugins.sources]]\nid='same'\ntype='javdb'\n[[plugins.sources]]\nid='same'\ntype='javdb'\n", true},
		{"unknown-type", "[[plugins.sources]]\nid='a'\ntype='unknown'\n", true},
		{"missing-default", "default_downloader='missing'\n", true},
		{"bad-type", "[[plugins.sources]]\nid='a'\ntype='nyaa'\nbase_url='https://nyaa.si'\nf='1'\n", true},
		{"unknown-key", "[[plugins.sources]]\nid='a'\ntype='javdb'\nsecret='x'\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			os.WriteFile(path, []byte(header+tc.body), 0600)
			cfg, err := LoadConfig(path, "")
			if (err != nil) != tc.bad {
				t.Fatalf("config err %v", err)
			}
			if tc.name == "multiple-shared-socket" && len(cfg.RCSockets()) != 1 {
				t.Fatal("shared socket not deduplicated")
			}
		})
	}
}
