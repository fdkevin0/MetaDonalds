package backend

import (
	"fmt"
	"github.com/fdkevin0/MetaDonalds/internal/plugins/javdb"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

type SourceConfig struct {
	ID      string `mapstructure:"id"`
	Type    string `mapstructure:"type"`
	Enabled *bool  `mapstructure:"enabled"`
	BaseURL string `mapstructure:"base_url"`
	F       int    `mapstructure:"f"`
	C       string `mapstructure:"c"`
}
type DownloaderConfig struct {
	ID       string `mapstructure:"id"`
	Type     string `mapstructure:"type"`
	Enabled  *bool  `mapstructure:"enabled"`
	RCSocket string `mapstructure:"rc_socket"`
	Remote   string `mapstructure:"remote"`
}
type PluginConfig struct {
	Sources     []SourceConfig     `mapstructure:"sources"`
	Downloaders []DownloaderConfig `mapstructure:"downloaders"`
}

func enabled(v *bool) bool { return v == nil || *v }

type DAVCacheConfig struct {
	Dir     string `mapstructure:"dir"`
	Refresh *bool  `mapstructure:"refresh"`
}

type Config struct {
	DAVCache DAVCacheConfig `mapstructure:"dav_cache"`
	Plugins  PluginConfig   `mapstructure:"plugins"`
	Server   struct {
		ListenAddr string `mapstructure:"listen_addr"`
	} `mapstructure:"server"`
}

func (cfg Config) Validate() error {
	if cfg.DAVCache.Dir != "" && !filepath.IsAbs(cfg.DAVCache.Dir) {
		return fmt.Errorf("dav_cache.dir must be an absolute path or empty")
	}
	return cfg.validatePlugins()
}

func (cfg Config) RCSockets() []string {
	out := []string{}
	seen := map[string]bool{}
	for _, d := range cfg.Plugins.Downloaders {
		if enabled(d.Enabled) && !seen[d.RCSocket] {
			out = append(out, d.RCSocket)
			seen[d.RCSocket] = true
		}
	}
	return out
}
func validURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && u.Fragment == "" && u.RawQuery == ""
}
func (cfg Config) validatePlugins() error {
	if strings.TrimSpace(cfg.Server.ListenAddr) == "" {
		return fmt.Errorf("server.listen_addr is required")
	}
	ids := map[string]bool{}
	validID := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	check := func(id string) error {
		if !validID.MatchString(id) || ids[id] {
			return fmt.Errorf("plugin IDs must be unique and contain 1–64 letters, digits, underscores or hyphens")
		}
		ids[id] = true
		return nil
	}
	for _, s := range cfg.Plugins.Sources {
		if err := check(s.ID); err != nil {
			return err
		}
		if s.Type != "nyaa" && s.Type != "javdb" {
			return fmt.Errorf("unknown source type %q", s.Type)
		}
		if !enabled(s.Enabled) {
			continue
		}
		base := s.BaseURL
		if base == "" && s.Type == "javdb" {
			base = javdb.DefaultURL
		}
		if !validURL(base) {
			return fmt.Errorf("source %s: base_url must be an HTTP(S) URL without query", s.ID)
		}
		if s.F < 0 || (s.C != "" && !regexp.MustCompile(`^[0-9]+_[0-9]+$`).MatchString(s.C)) {
			return fmt.Errorf("source %s: invalid filter/category", s.ID)
		}
	}
	for _, d := range cfg.Plugins.Downloaders {
		if err := check(d.ID); err != nil {
			return err
		}
		if d.Type != "pikpak-rclone" {
			return fmt.Errorf("unknown downloader type %q", d.Type)
		}
		if !enabled(d.Enabled) {
			continue
		}
		if !filepath.IsAbs(d.RCSocket) || !strings.Contains(d.Remote, ":") {
			return fmt.Errorf("downloader %s requires absolute rc_socket and remote:path", d.ID)
		}
	}
	return nil
}
