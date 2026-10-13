package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/backend"
	"github.com/fdkevin0/MetaDonalds/internal/client"
)

// Ensure reuses a healthy server, or starts an HTTP server at a loopback origin.
// The returned function stops only the server this call started.
func Ensure(ctx context.Context, serverURL, configPath string) (func(), error) {
	u, err := client.ParseURL(serverURL)
	if err != nil {
		return nil, err
	}
	serverURL = u.String()
	if err := health(ctx, serverURL); err == nil {
		return func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "http" || (!strings.EqualFold(u.Hostname(), "localhost") && !ip.IsLoopback()) {
		return nil, fmt.Errorf("server %s is unavailable; automatic startup requires a local HTTP loopback address", serverURL)
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	if port == "0" {
		return nil, fmt.Errorf("server URL must specify a nonzero port")
	}
	bindHost := u.Hostname()
	if strings.EqualFold(bindHost, "localhost") {
		bindHost = "127.0.0.1"
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort(bindHost, port))
	if err != nil {
		// Another UI may have bound the address but not finished starting yet.
		if waitHealthy(ctx, serverURL) == nil {
			return func() {}, nil
		}
		return nil, fmt.Errorf("cannot start server at %s (port occupied or unavailable): %w", serverURL, err)
	}
	cfg, err := LoadConfig(configPath, "")
	if err != nil {
		listener.Close()
		return nil, fmt.Errorf("cannot start local server: %w", err)
	}
	// File logging keeps the embedded backend from corrupting the terminal UI.
	logs, err := os.CreateTemp("", "metadonalds-server-*.log")
	if err != nil {
		listener.Close()
		return nil, fmt.Errorf("create server log: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	processes := startDownloaders(ctx, cfg, logger, logs)
	lifetime, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		if err := serve(lifetime, listener, backend.Handler(cfg, logger), nil); err != nil {
			logger.Error("HTTP server stopped", "error", err)
		}
		close(done)
	}()
	stop := sync.OnceFunc(func() {
		cancel()
		<-done
		stopDownloaders(processes)
		logs.Close()
	})
	if err := waitHealthy(ctx, serverURL); err != nil {
		stop()
		return nil, fmt.Errorf("local server failed to become ready (log: %s): %w", logs.Name(), err)
	}
	slog.Info("started local MetaDonalds server", "url", serverURL, "log", logs.Name())
	return stop, nil
}

func health(ctx context.Context, serverURL string) error {
	ctx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/healthz", nil)
	if err != nil {
		return err
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := c.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 16))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || string(body) != "ok\n" {
		return fmt.Errorf("unexpected /healthz response (HTTP %d)", response.StatusCode)
	}
	return nil
}

func waitHealthy(ctx context.Context, serverURL string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := health(ctx, serverURL); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
