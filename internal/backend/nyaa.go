package backend

import (
	"context"
	"github.com/fdkevin0/MetaDonalds/internal/core"
	"github.com/fdkevin0/MetaDonalds/internal/plugins/nyaa"
	"net/http"
)

type torrent = nyaa.Torrent

func searchNyaa(ctx context.Context, c *http.Client, cfg NyaaConfig, q string) ([]torrent, error) {
	return nyaa.Search(ctx, c, cfg, q)
}

var infoHashPattern = core.HashPattern
