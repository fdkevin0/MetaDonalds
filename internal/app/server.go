package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/backend"
)

// Serve runs a standalone backend until cancellation, then drains active requests.
func Serve(ctx context.Context, cfg backend.Config) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Server.ListenAddr)
	if err != nil {
		return err
	}
	defer listener.Close()
	rclone, err := startRclone(ctx, cfg.PikPak.RCSocket, slog.Default(), os.Stderr)
	if err != nil {
		return err
	}
	slog.Info("starting MetaDonalds", "listen_addr", listener.Addr().String())
	return serve(ctx, listener, backend.Handler(cfg, slog.Default()), rclone)
}

// Both standalone and auto-started servers use the same timeouts and shutdown path.
func serve(ctx context.Context, listener net.Listener, handler http.Handler, rclone *rcloneProcess) error {
	defer rclone.stop()
	var childDone <-chan struct{}
	if rclone != nil {
		childDone = rclone.done
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	var stopped error
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	case <-childDone:
		stopped = fmt.Errorf("rclone rcd exited unexpectedly: %v", rclone.err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)
	if err != nil {
		_ = srv.Close()
	}
	<-done
	return errors.Join(stopped, err)
}
