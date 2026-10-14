package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fdkevin0/MetaDonalds/internal/backend"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// LoadConfig preserves strict TOML validation, then applies environment and CLI overrides.
func LoadConfig(path, listenOverride string) (backend.Config, error) {
	var cfg backend.Config
	if path == "" {
		var err error
		path, err = discoverConfig()
		if err != nil {
			return cfg, err
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
	if cacheDir, err := os.UserCacheDir(); err == nil {
		v.SetDefault("dav_cache.dir", filepath.Join(cacheDir, "metadonalds", "dav"))
	}
	v.SetDefault("dav_cache.refresh", true)
	v.SetEnvPrefix("METADONALDS")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AllowEmptyEnv(true)
	for _, key := range []string{"dav_cache.dir", "dav_cache.refresh", "server.listen_addr"} {
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
	for i := range cfg.Plugins.Downloaders {
		if cfg.Plugins.Downloaders[i].RCSocket == "" {
			cfg.Plugins.Downloaders[i].RCSocket = filepath.Join(os.TempDir(), fmt.Sprintf("metadonalds-%d", os.Getuid()), "rclone.sock")
		}
	}
	return cfg, cfg.Validate()
}
