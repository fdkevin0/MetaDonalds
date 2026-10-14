package app

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

// First-run defaults enable Nyaa, JavDB and PikPak without exposing the local API.
const defaultConfig = `# Enabled source and downloader instances.
[server]
listen_addr = "127.0.0.1:8080"

[[plugins.sources]]
id = "nyaa"
type = "nyaa"
base_url = "https://sukebei.nyaa.si/"
f = 0
c = "0_0"

[[plugins.sources]]
id = "catalog"
type = "javdb"

[[plugins.downloaders]]
id = "pikpak"
type = "pikpak-rclone"
# Configure this remote with rclone config before submitting downloads.
# rc_socket defaults to a private per-user temporary directory.
remote = "pikpak:My Pack"
`

func discoverConfig() (string, error) {
	candidates := []string{"config.toml"}
	for _, base := range append([]string{xdg.ConfigHome}, xdg.ConfigDirs...) {
		candidates = append(candidates, filepath.Join(base, "metadonalds", "config.toml"))
	}
	for _, path := range candidates {
		// An existing broken symlink or unreadable entry is an error, not permission to initialize.
		if _, err := os.Lstat(path); err == nil {
			return path, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect configuration %q: %w", path, err)
		}
	}
	path := filepath.Join(xdg.ConfigHome, "metadonalds", "config.toml")
	if err := initializeConfig(path); err != nil {
		return "", fmt.Errorf("initialize configuration %q: %w", path, err)
	}
	return path, nil
}

func initializeConfig(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.WriteString(defaultConfig); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	// Publish a complete 0600 file atomically without replacing another process's config.
	if err = os.Link(file.Name(), path); err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	slog.Info("initialized default configuration", "path", path)
	return nil
}
