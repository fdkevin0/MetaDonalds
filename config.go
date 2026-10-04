package main

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type nyaaConfig struct {
	BaseURL string `toml:"base_url"`
	F       int    `toml:"f"`
	C       string `toml:"c"`
}

type config struct {
	Server struct {
		ListenAddr string `toml:"listen_addr"`
	} `toml:"server"`
	Nyaa   nyaaConfig `toml:"nyaa"`
	PikPak struct {
		RCSocket string `toml:"rc_socket"`
		Remote   string `toml:"remote"`
	} `toml:"pikpak"`
}

func loadConfig(path string) (config, error) {
	cfg := config{Nyaa: nyaaConfig{F: 0, C: "0_0"}}
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return cfg, err
	}
	if keys := meta.Undecoded(); len(keys) != 0 {
		return cfg, fmt.Errorf("unknown config keys: %v", keys)
	}
	u, err := url.Parse(cfg.Nyaa.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return cfg, fmt.Errorf("nyaa.base_url must be an absolute HTTP(S) URL")
	}
	if strings.TrimSpace(cfg.Server.ListenAddr) == "" {
		return cfg, fmt.Errorf("server.listen_addr is required")
	}
	if !filepath.IsAbs(cfg.PikPak.RCSocket) {
		return cfg, fmt.Errorf("pikpak.rc_socket must be an absolute path")
	}
	if !strings.Contains(cfg.PikPak.Remote, ":") {
		return cfg, fmt.Errorf("pikpak.remote must be an rclone remote:path")
	}
	return cfg, nil
}
