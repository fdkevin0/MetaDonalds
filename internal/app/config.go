package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrg/xdg"
	"github.com/fdkevin0/MetaDonalds/internal/backend"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// LoadConfig preserves strict TOML validation, then applies environment and CLI overrides.
func LoadConfig(path, listenOverride string) (backend.Config, error) {
	var cfg backend.Config
	if path == "" {
		path = "config.toml"
		if _, err := os.Stat(path); os.IsNotExist(err) {
			path, err = xdg.SearchConfigFile("metadonalds/config.toml")
			if err != nil {
				return cfg, fmt.Errorf("find configuration in current directory or XDG paths (use --config to specify a file): %w", err)
			}
		}
	}
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("toml")
	if err := v.ReadInConfig(); err != nil {
		return cfg, fmt.Errorf("read --config %q: %w", path, err)
	}
	// Validate file types before enabling string-valued environment overrides.
	if err := v.UnmarshalExact(&cfg, func(c *mapstructure.DecoderConfig) { c.WeaklyTypedInput = false }); err != nil {
		return cfg, fmt.Errorf("invalid configuration %q: %w", path, err)
	}
	v.SetDefault("pikpak.rc_socket", filepath.Join(os.TempDir(), fmt.Sprintf("metadonalds-%d", os.Getuid()), "rclone.sock"))
	v.SetDefault("nyaa.f", 0)
	v.SetDefault("nyaa.c", "0_0")
	v.SetEnvPrefix("METADONALDS")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AllowEmptyEnv(true)
	for _, key := range []string{"server.listen_addr", "nyaa.base_url", "nyaa.f", "nyaa.c", "pikpak.rc_socket", "pikpak.remote"} {
		if err := v.BindEnv(key); err != nil {
			return cfg, err
		}
	}
	if listenOverride != "" {
		v.Set("server.listen_addr", listenOverride)
	}
	if err := v.UnmarshalExact(&cfg); err != nil {
		return cfg, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, cfg.Validate()
}
