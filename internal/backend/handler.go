package backend

import (
	"context"
	"fmt"
	"github.com/fdkevin0/MetaDonalds/internal/core"
	"github.com/fdkevin0/MetaDonalds/internal/plugins/javdb"
	"github.com/fdkevin0/MetaDonalds/internal/plugins/nyaa"
	"github.com/fdkevin0/MetaDonalds/internal/plugins/pikpak"
	"log/slog"
	"net/http"
	"time"
)

// Handler assembles compiled plugins; business routes depend only on core capabilities.
func Handler(cfg Config, logger *slog.Logger) http.Handler {
	c := &http.Client{Timeout: 15 * time.Second}
	registry := core.NewRegistry()
	instances := cfg.Instances()
	s := server{logger: logger, registry: registry}
	for _, instance := range instances.Sources {
		if !enabled(instance.Enabled) {
			continue
		}
		switch instance.Type {
		case "nyaa":
			nc := nyaa.Config{BaseURL: instance.BaseURL, F: instance.F, C: instance.C}
			if nc.C == "" {
				nc.C = "0_0"
			}
			registry.AddSource(instance.ID, instance.Type, []string{"magnet"}, &nyaa.Source{ID: instance.ID, Config: nc, Client: c})
			if instance.ID == instances.DefaultSource {
				s.nyaa = nc
				s.search = func(ctx context.Context, cfg NyaaConfig, q string) ([]torrent, error) {
					return searchNyaa(ctx, c, cfg, q)
				}
			}
		case "javdb":
			base := instance.BaseURL
			if base == "" {
				base = javdb.DefaultURL
			}
			registry.AddSource(instance.ID, instance.Type, []string{"magnet"}, &javdb.Source{ID: instance.ID, BaseURL: base, Client: c})
		}
	}
	for _, d := range instances.Downloaders {
		if !enabled(d.Enabled) {
			continue
		}
		downloader := &pikpak.Downloader{Remote: d.Remote, Client: pikpak.NewRCClient(d.RCSocket)}
		registry.AddDownloader(d.ID, d.Type, []string{"magnet"}, downloader)
		if d.ID == instances.DefaultDownloader {
			s.submit = func(ctx context.Context, hash string) error {
				result := downloader.Submit(ctx, core.Resource{Kind: "magnet", InfoHash: hash})
				if result.Status != "accepted" {
					return fmt.Errorf("submission not confirmed")
				}
				return nil
			}
		}
	}
	return s.routes()
}
