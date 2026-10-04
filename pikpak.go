package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

func newRCClient(socket string) *http.Client {
	return &http.Client{
		Timeout: 45 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}},
	}
}

func submitViaRC(ctx context.Context, client *http.Client, remote, hash string) error {
	body, err := json.Marshal(struct {
		Command string   `json:"command"`
		FS      string   `json:"fs"`
		Arg     []string `json:"arg"`
	}{"addurl", remote, []string{"magnet:?xt=urn:btih:" + hash}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://rclone/backend/command", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rclone RC returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var output struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &output); err != nil || len(output.Result) == 0 {
		return fmt.Errorf("invalid rclone RC response: %s", strings.TrimSpace(string(data)))
	}
	return nil
}
