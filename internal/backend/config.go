package backend

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

type NyaaConfig struct {
	BaseURL string `mapstructure:"base_url"`
	F       int    `mapstructure:"f"`
	C       string `mapstructure:"c"`
}

type Config struct {
	Server struct {
		ListenAddr string `mapstructure:"listen_addr"`
	} `mapstructure:"server"`
	Nyaa   NyaaConfig `mapstructure:"nyaa"`
	PikPak struct {
		RCSocket string `mapstructure:"rc_socket"`
		Remote   string `mapstructure:"remote"`
	} `mapstructure:"pikpak"`
}

func (cfg Config) Validate() error {
	u, err := url.Parse(cfg.Nyaa.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("nyaa.base_url must be an absolute HTTP(S) URL")
	}
	if strings.TrimSpace(cfg.Server.ListenAddr) == "" {
		return fmt.Errorf("server.listen_addr is required")
	}
	if !filepath.IsAbs(cfg.PikPak.RCSocket) {
		return fmt.Errorf("pikpak.rc_socket must be an absolute path")
	}
	if !strings.Contains(cfg.PikPak.Remote, ":") {
		return fmt.Errorf("pikpak.remote must be an rclone remote:path")
	}
	return nil
}
