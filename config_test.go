package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfig(t *testing.T) {
	if _, err := loadConfig("config.toml"); err != nil {
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
	cfg, err := loadConfig(path)
	if err != nil || cfg.Nyaa.BaseURL != "https://mirror.example/nyaa/" || cfg.Nyaa.F != 0 || cfg.Nyaa.C != "0_0" {
		t.Fatalf("config: %#v, %v", cfg, err)
	}
	custom := strings.Replace(content, "[nyaa]", "[nyaa]\nf = 2\nc = \"1_0\"", 1)
	if err := os.WriteFile(path, []byte(custom), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig(path)
	if err != nil || cfg.Nyaa.F != 2 || cfg.Nyaa.C != "1_0" {
		t.Fatalf("custom Nyaa config: %#v, %v", cfg.Nyaa, err)
	}
	if err := os.WriteFile(path, []byte(content+"unexpected = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Fatal("unknown config key accepted")
	}
}
