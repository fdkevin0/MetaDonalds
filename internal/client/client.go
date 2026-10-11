// Package client connects user interfaces to an existing MetaDonalds service.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultURL = "http://127.0.0.1:8080"

type Torrent struct {
	Title    string `json:"title"`
	InfoHash string `json:"info_hash"`
	Seeders  int    `json:"seeders"`
	Size     string `json:"size"`
	Trusted  bool   `json:"trusted"`
	Remake   bool   `json:"remake"`
	Score    int    `json:"score"`
}

func ParseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("server must be an HTTP(S) origin, e.g. %s", DefaultURL)
	}
	u.Path = ""
	return u, nil
}

type Client struct {
	base string
	http *http.Client
}

func New(raw string) (*Client, error) {
	u, err := ParseURL(raw)
	if err != nil {
		return nil, err
	}
	return &Client{base: u.String(), http: &http.Client{
		Timeout:       60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) Search(ctx context.Context, query, filter, category string) ([]Torrent, error) {
	query = strings.TrimSpace(query)
	if len(query) == 0 || len(query) > 200 {
		return nil, fmt.Errorf("search must be 1–200 bytes after trimming")
	}
	params := url.Values{"q": {query}}
	if filter != "" {
		params.Set("f", filter)
	}
	if category != "" {
		params.Set("c", category)
	}
	var out struct {
		Results []Torrent `json:"results"`
	}
	err := c.request(ctx, http.MethodGet, "/api/v1/search?"+params.Encode(), nil, &out)
	if err == nil && out.Results == nil {
		err = fmt.Errorf("invalid search response")
	}
	return out.Results, err
}

func (c *Client) Submit(ctx context.Context, hash string) error {
	body, err := json.Marshal(map[string]string{"info_hash": hash})
	if err != nil {
		return err
	}
	var response struct {
		Status   string `json:"status"`
		InfoHash string `json:"info_hash"`
	}
	if err := c.request(ctx, http.MethodPost, "/api/v1/submit", body, &response); err != nil {
		return fmt.Errorf("submission not confirmed; check PikPak before retrying: %w", err)
	}
	if response.Status != "submitted" || response.InfoHash != strings.ToLower(hash) {
		return fmt.Errorf("invalid submission response; check PikPak before retrying")
	}
	return nil
}

func (c *Client) request(ctx context.Context, method, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var problem struct{ Detail, Title string }
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&problem)
		message := problem.Detail
		if message == "" {
			message = problem.Title
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, message)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}
