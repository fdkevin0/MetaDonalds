package backend

import (
	"fmt"
	"github.com/fdkevin0/MetaDonalds/internal/plugins/javdb"
	"github.com/fdkevin0/MetaDonalds/internal/plugins/nyaa"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

type NyaaConfig = nyaa.Config

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
	Sources           []SourceConfig     `mapstructure:"sources"`
	Downloaders       []DownloaderConfig `mapstructure:"downloaders"`
	DefaultSource     string             `mapstructure:"default_source"`
	DefaultDownloader string             `mapstructure:"default_downloader"`
}

func enabled(v *bool) bool { return v == nil || *v }

type Config struct {
	Plugins *PluginConfig `mapstructure:"plugins"`
	Server  struct {
		ListenAddr string `mapstructure:"listen_addr"`
	} `mapstructure:"server"`
	Nyaa   NyaaConfig `mapstructure:"nyaa"`
	PikPak struct {
		RCSocket string `mapstructure:"rc_socket"`
		Remote   string `mapstructure:"remote"`
	} `mapstructure:"pikpak"`
}

func (cfg Config) Validate() error {
	if cfg.Plugins != nil {
		return cfg.validatePlugins()
	}
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

// Instances preserves legacy configuration only when the plugins table is absent.
func (cfg Config) Instances() PluginConfig {
	if cfg.Plugins != nil {
		return *cfg.Plugins
	}
	return PluginConfig{DefaultSource: "nyaa", DefaultDownloader: "pikpak", Sources: []SourceConfig{{ID: "nyaa", Type: "nyaa", BaseURL: cfg.Nyaa.BaseURL, F: cfg.Nyaa.F, C: cfg.Nyaa.C}}, Downloaders: []DownloaderConfig{{ID: "pikpak", Type: "pikpak-rclone", RCSocket: cfg.PikPak.RCSocket, Remote: cfg.PikPak.Remote}}}
}
func (cfg Config) RCSockets() []string {
	out := []string{}
	seen := map[string]bool{}
	for _, d := range cfg.Instances().Downloaders {
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
	sources := map[string]bool{}
	downloaders := map[string]bool{}
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
		sources[s.ID] = true
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
		downloaders[d.ID] = true
	}
	if d := cfg.Plugins.DefaultSource; d != "" && !sources[d] {
		return fmt.Errorf("default_source must name an enabled source")
	}
	if d := cfg.Plugins.DefaultDownloader; d != "" && !downloaders[d] {
		return fmt.Errorf("default_downloader must name an enabled downloader")
	}
	if d := cfg.Plugins.DefaultSource; d != "" {
		for _, s := range cfg.Plugins.Sources {
			if s.ID == d && s.Type != "nyaa" {
				return fmt.Errorf("legacy default_source must be a Nyaa instance")
			}
		}
	}
	return nil
}
