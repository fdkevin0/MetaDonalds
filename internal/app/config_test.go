package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
	"github.com/fdkevin0/MetaDonalds/internal/backend"
)

func TestConfig(t *testing.T) {
	if _, err := LoadConfig("../../config.example.toml", ""); err != nil {
		t.Fatal(err)
	}
	header := "[server]\nlisten_addr='127.0.0.1:8080'\n"
	for _, tc := range []struct {
		name, body string
		bad        bool
	}{
		{"empty", "[plugins]\n", false},
		{"search-only", "[[plugins.sources]]\nid='catalog'\ntype='javdb'\n", false},
		{"no-implicit-sources", "[[plugins.sources]]\nid='only-nyaa'\ntype='nyaa'\nbase_url='https://nyaa.si/'\n", false},
		{"duplicate", "[[plugins.sources]]\nid='same'\ntype='javdb'\n[[plugins.sources]]\nid='same'\ntype='javdb'\n", true},
		{"unknown-type", "[[plugins.sources]]\nid='a'\ntype='unknown'\n", true},
		{"negative-filter", "[[plugins.sources]]\nid='n'\ntype='nyaa'\nbase_url='https://nyaa.si/'\nf=-1\n", true},
		{"bad-category", "[[plugins.sources]]\nid='n'\ntype='nyaa'\nbase_url='https://nyaa.si/'\nc='broken'\n", true},
		{"bad-type", "[[plugins.sources]]\nid='n'\ntype='nyaa'\nbase_url='https://nyaa.si/'\nf='1'\n", true},
		{"unknown-key", "[[plugins.sources]]\nid='catalog'\ntype='javdb'\nsecret='x'\n", true},
		{"removed-source-config", "[nyaa]\nbase_url='https://nyaa.si/'\n", true},
		{"removed-downloader-config", "[pikpak]\nremote='pikpak:'\n", true},
		{"removed-default", "[plugins]\ndefault_source='nyaa'\n", true},
		{"relative-socket", "[[plugins.downloaders]]\nid='d'\ntype='pikpak-rclone'\nremote='pikpak:'\nrc_socket='relative.sock'\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(header+tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path, "")
			if (err != nil) != tc.bad {
				t.Fatalf("config=%+v error=%v", cfg, err)
			}
			if tc.name == "no-implicit-sources" && (len(cfg.Plugins.Sources) != 1 || len(cfg.Plugins.Downloaders) != 0) {
				t.Fatal("instances added implicitly")
			}
		})
	}
}

func TestDAVCacheConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	base := "[server]\nlisten_addr = '127.0.0.1:8080'\n[plugins]\n"
	write := func(extra string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(base+extra), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("")
	cfg, err := LoadConfig(path, "")
	if err != nil || !filepath.IsAbs(cfg.DAVCache.Dir) || cfg.DAVCache.Refresh == nil || !*cfg.DAVCache.Refresh {
		t.Fatalf("defaults: %+v %v", cfg.DAVCache, err)
	}
	write("[dav_cache]\ndir = ''\nrefresh = false\n")
	cfg, err = LoadConfig(path, "")
	if err != nil || cfg.DAVCache.Dir != "" || cfg.DAVCache.Refresh == nil || *cfg.DAVCache.Refresh {
		t.Fatalf("disabled: %+v %v", cfg.DAVCache, err)
	}
	write("[dav_cache]\ndir = 'relative'\n")
	if _, err := LoadConfig(path, ""); err == nil {
		t.Fatal("relative cache directory accepted")
	}
	write("[dav_cache]\nrefresh = 'false'\n")
	if _, err := LoadConfig(path, ""); err == nil {
		t.Fatal("string boolean accepted")
	}
	write("")
	dir := t.TempDir()
	t.Setenv("METADONALDS_DAV_CACHE_DIR", dir)
	t.Setenv("METADONALDS_DAV_CACHE_REFRESH", "false")
	cfg, err = LoadConfig(path, "")
	if err != nil || cfg.DAVCache.Dir != dir || cfg.DAVCache.Refresh == nil || *cfg.DAVCache.Refresh {
		t.Fatalf("environment: %+v %v", cfg.DAVCache, err)
	}
}

func TestConfigOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[server]\nlisten_addr='127.0.0.1:8080'\n[[plugins.downloaders]]\nid='one'\ntype='pikpak-rclone'\nremote='pikpak:'\n[[plugins.downloaders]]\nid='two'\ntype='pikpak-rclone'\nremote='pikpak:other'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("METADONALDS_SERVER_LISTEN_ADDR", "127.0.0.1:9090")
	cfg, err := LoadConfig(path, "")
	if err != nil || cfg.Server.ListenAddr != "127.0.0.1:9090" || len(cfg.RCSockets()) != 1 || !filepath.IsAbs(cfg.Plugins.Downloaders[0].RCSocket) {
		t.Fatalf("config=%+v error=%v", cfg, err)
	}
	cfg, err = LoadConfig(path, "127.0.0.1:7070")
	if err != nil || cfg.Server.ListenAddr != "127.0.0.1:7070" {
		t.Fatalf("config=%+v error=%v", cfg, err)
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
		data := "[server]\nlisten_addr = '127.0.0.1:8080'\n[[plugins.downloaders]]\nid='pikpak'\ntype='pikpak-rclone'\nrc_socket = '/tmp/rclone.sock'\nremote = '" + remote + ":'\n"
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(path, remote string) {
		t.Helper()
		cfg, err := LoadConfig(path, "")
		if err != nil || cfg.Plugins.Downloaders[0].Remote != remote+":" {
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

func assertDefaultInstances(t *testing.T, cfg backend.Config) {
	t.Helper()
	p := cfg.Plugins
	if len(p.Sources) != 2 || len(p.Downloaders) != 1 || p.Sources[0].ID != "nyaa" || p.Sources[0].Type != "nyaa" || p.Sources[0].BaseURL != "https://sukebei.nyaa.si/" || p.Sources[1].ID != "catalog" || p.Sources[1].Type != "javdb" || p.Downloaders[0].ID != "pikpak" || p.Downloaders[0].Type != "pikpak-rclone" || !filepath.IsAbs(p.Downloaders[0].RCSocket) {
		t.Fatalf("unexpected instances: %+v", p)
	}
}

func TestExampleInstances(t *testing.T) {
	path := "../../config.example.toml"
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path, "")
	if err != nil {
		t.Fatal(err)
	}
	assertDefaultInstances(t, cfg)
	if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
		t.Fatal("example rewritten")
	}
}
