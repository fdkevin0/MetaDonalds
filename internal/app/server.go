package app

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/backend"
)

// Serve runs a standalone backend until cancellation, then drains active requests.
// If set, ready is called with the browser URL after HTTP starts accepting requests.
func Serve(ctx context.Context, cfg backend.Config, ready func(string)) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Server.ListenAddr)
	if err != nil {
		return err
	}
	defer listener.Close()
	processes := startDownloaders(ctx, cfg, slog.Default(), os.Stderr)
	defer stopDownloaders(processes)
	slog.Info("starting MetaDonalds", "listen_addr", listener.Addr().String())
	if ready == nil {
		return serve(ctx, listener, backend.Handler(cfg, slog.Default()), nil)
	}
	listening := &readyListener{Listener: listener, ready: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- serve(ctx, listening, backend.Handler(cfg, slog.Default()), nil) }()
	select {
	case err := <-done:
		return err
	case <-listening.ready:
		if ctx.Err() == nil {
			ready(browserURL(cfg.Server.ListenAddr, listener.Addr().(*net.TCPAddr)) + "/")
		}
	case <-ctx.Done():
	}
	return <-done
}

// Serve calls Accept only after its HTTP handler and accept loop are initialized.
// The socket is already listening, so browser requests can connect immediately.
// This local handshake also works when environment proxies cannot reach the bind IP.
type readyListener struct {
	net.Listener
	ready chan struct{}
	once  sync.Once
}

func (l *readyListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.ready) })
	return l.Listener.Accept()
}

// Use the bound port (including :0), and replace wildcard hosts with loopback.
func browserURL(listen string, bound *net.TCPAddr) string {
	host, _, _ := net.SplitHostPort(listen)
	ip := bound.IP
	if host == "0.0.0.0" {
		ip = net.IPv4(127, 0, 0, 1)
	} else if ip.IsUnspecified() {
		if ip.To4() != nil {
			ip = net.IPv4(127, 0, 0, 1)
		} else {
			ip = net.IPv6loopback
		}
	}
	address := &net.TCPAddr{IP: ip, Port: bound.Port, Zone: bound.Zone}
	return (&url.URL{Scheme: "http", Host: address.String()}).String()
}

// Both standalone and auto-started servers use the same timeouts and shutdown path.
func serve(ctx context.Context, listener net.Listener, handler http.Handler, rclone *rcloneProcess) error {
	defer rclone.stop()
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := srv.Shutdown(shutdownCtx)
	if err != nil {
		_ = srv.Close()
	}
	<-done
	return err
}
