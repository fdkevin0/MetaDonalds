package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	configPath := flag.String("config", "config.toml", "service configuration file")
	flag.Parse()
	cfg, err := loadConfig(*configPath)
	if err != nil {
		slog.Error("configuration failed", "error", err)
		os.Exit(1)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	rcClient := newRCClient(cfg.PikPak.RCSocket)
	s := server{
		nyaa: cfg.Nyaa,
		search: func(ctx context.Context, searchConfig nyaaConfig, query string) ([]torrent, error) {
			return searchNyaa(ctx, client, searchConfig, query)
		},
		submit: func(ctx context.Context, hash string) error {
			return submitViaRC(ctx, rcClient, cfg.PikPak.Remote, hash)
		},
	}
	slog.Info("starting MetaDonalds", "listen_addr", cfg.Server.ListenAddr)
	if err := http.ListenAndServe(cfg.Server.ListenAddr, s.routes()); err != nil {
		slog.Error("HTTP server stopped", "error", err)
		os.Exit(1)
	}
}
