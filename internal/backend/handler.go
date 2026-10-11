package backend

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Handler builds the shared HTTP API and WebUI.
func Handler(cfg Config, logger *slog.Logger) http.Handler {
	client := &http.Client{Timeout: 15 * time.Second}
	rcClient := NewRCClient(cfg.PikPak.RCSocket)
	return (server{
		logger: logger,
		nyaa:   cfg.Nyaa,
		search: func(ctx context.Context, searchConfig NyaaConfig, query string) ([]torrent, error) {
			return searchNyaa(ctx, client, searchConfig, query)
		},
		submit: func(ctx context.Context, hash string) error {
			return submitViaRC(ctx, rcClient, cfg.PikPak.Remote, hash)
		},
	}).routes()
}
