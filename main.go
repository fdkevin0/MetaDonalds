package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"time"
)

func main() {
	configPath := flag.String("config", "config.toml", "service configuration file")
	flag.Parse()
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	rcClient := newRCClient(cfg.PikPak.RCSocket)
	s := server{
		search: func(ctx context.Context, query string) ([]torrent, error) {
			return searchNyaa(ctx, client, cfg.Nyaa.BaseURL, query)
		},
		submit: func(ctx context.Context, hash string) error {
			return submitViaRC(ctx, rcClient, cfg.PikPak.Remote, hash)
		},
	}
	log.Printf("listening on %s", cfg.Server.ListenAddr)
	log.Fatal(http.ListenAndServe(cfg.Server.ListenAddr, s.routes()))
}
