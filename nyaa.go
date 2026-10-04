package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var infoHashPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

type torrent struct {
	Title    string `json:"title"`
	InfoHash string `json:"info_hash"`
	Seeders  int    `json:"seeders"`
	Size     string `json:"size"`
	Trusted  bool   `json:"trusted"`
	Remake   bool   `json:"remake"`
	Score    int    `json:"score"`
}

type rssFeed struct {
	Items []struct {
		Title    string `xml:"title"`
		InfoHash string `xml:"infoHash"`
		Seeders  string `xml:"seeders"`
		Size     string `xml:"size"`
		Trusted  string `xml:"trusted"`
		Remake   string `xml:"remake"`
	} `xml:"channel>item"`
}

func searchNyaa(ctx context.Context, client *http.Client, cfg nyaaConfig, query string) ([]torrent, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	params := u.Query()
	params.Set("page", "rss")
	params.Set("f", strconv.Itoa(cfg.F))
	params.Set("c", cfg.C)
	params.Set("q", query)
	u.RawQuery = params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Nyaa returned HTTP %d", resp.StatusCode)
	}
	var feed rssFeed
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&feed); err != nil {
		return nil, err
	}
	results := make([]torrent, 0, len(feed.Items))
	for _, item := range feed.Items {
		hash := strings.ToLower(strings.TrimSpace(item.InfoHash))
		if !infoHashPattern.MatchString(hash) {
			continue
		}
		seeders, _ := strconv.Atoi(strings.TrimSpace(item.Seeders))
		t := torrent{
			Title: strings.TrimSpace(item.Title), InfoHash: hash,
			Seeders: seeders, Size: strings.TrimSpace(item.Size),
			Trusted: strings.EqualFold(strings.TrimSpace(item.Trusted), "yes"),
			Remake:  strings.EqualFold(strings.TrimSpace(item.Remake), "yes"),
		}
		// ponytail: simple ranking only; add configurable rules when real searches show a need.
		t.Score = min(max(seeders, 0), 100)
		if t.Trusted {
			t.Score += 20
		}
		if t.Remake {
			t.Score -= 100
		}
		results = append(results, t)
	}
	slices.SortStableFunc(results, func(a, b torrent) int { return b.Score - a.Score })
	return results, nil
}
