package backend

import (
	"context"
	"log/slog"
	"os"
	"path"
	"strings"
	"time"
)

// flatLoader rebuilds a bounded read from a validated directory, never a stored URL.
func (d *davServer) flatLoader(key string) (func(context.Context) (*flatSnapshot, error), error) {
	kind, name, ok := strings.Cut(key, ":")
	if !ok || !strings.HasPrefix(name, "/") || path.Clean(name) != name {
		return nil, os.ErrInvalid
	}
	parts := strings.Split(strings.TrimPrefix(name, "/"), "/")
	if len(parts) < 2 || d.catalogs[parts[0]] == nil {
		return nil, os.ErrInvalid
	}
	catalog := d.catalogs[parts[0]]
	if kind == "movies" && (parts[1] == "latest" && len(parts) == 2 || parts[1] == "search" && len(parts) == 3 && validSearchPath(parts[2])) {
		query := ""
		if len(parts) == 3 {
			query = parts[2]
		}
		return func(ctx context.Context) (*flatSnapshot, error) {
			return d.flatLatestOrSearch(ctx, catalog, name, parts[1], query)
		}, nil
	}
	if !browseSection(parts[1]) {
		return nil, os.ErrInvalid
	}
	r, err := parseDAVBrowse(parts)
	if err != nil {
		return nil, err
	}
	switch {
	case kind == "entities" && r.node == "entities-type":
		return func(ctx context.Context) (*flatSnapshot, error) {
			return d.flatEntities(ctx, name, catalog, r.section, r.typ)
		}, nil
	case kind == "tags" && r.node == "categories-tags":
		return func(ctx context.Context) (*flatSnapshot, error) { return d.flatTags(ctx, name, catalog, r.typ) }, nil
	case kind == "movies" && (r.node == "categories-type" || r.node == "categories-tag" || r.node == "rankings-period" || r.node == "entities-person" || r.node == "rankings-type" && r.kind == "top250"):
		return func(ctx context.Context) (*flatSnapshot, error) { return d.flatBrowseMovies(ctx, catalog, name, r) }, nil
	}
	return nil, os.ErrInvalid
}

func (d *davServer) startCache(cfg DAVCacheConfig, fingerprints map[string]string, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	c := &d.flat
	c.logger = logger
	c.lifetime, c.cancel = context.WithCancel(context.Background())
	c.activity = map[string]flatActivity{}
	if cfg.Dir != "" {
		disk, err := openFlatDisk(cfg.Dir, fingerprints)
		if err != nil {
			logger.Warn("DAV cache persistence unavailable; using memory", "operation", "open", "error", err)
		} else {
			c.disk = disk
			disk.restore(d)
		}
	}
	if !enabled(cfg.Refresh) {
		return
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(flatTick)
		defer ticker.Stop()
		for {
			select {
			case <-c.lifetime.Done():
				return
			case now := <-ticker.C:
				key := c.due(now)
				if key == "" {
					continue
				}
				load, err := d.flatLoader(key)
				if err == nil {
					_, _ = load(context.WithValue(c.lifetime, flatRefreshKey{}, true))
				}
			}
		}
	}()
}
