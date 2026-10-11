package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/backend"
)

type rcloneProcess struct {
	cmd        *exec.Cmd
	done       chan struct{}
	err        error // Read only after done closes.
	socket     string
	socketInfo os.FileInfo
}

// startRclone returns nil when an existing rclone can be reused.
func startRclone(ctx context.Context, socket string, logger *slog.Logger, output io.Writer) (*rcloneProcess, error) {
	client := backend.NewRCClient(socket)
	defer client.CloseIdleConnections()
	if err := rcloneHealth(ctx, client); err == nil {
		logger.Info("reusing rclone", "socket", socket)
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A failed HTTP health check alone does not establish that a socket is stale.
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("rclone socket path %q is occupied by a non-socket file", socket)
		}
		conn, dialErr := (&net.Dialer{Timeout: 500 * time.Millisecond}).DialContext(ctx, "unix", socket)
		if dialErr == nil {
			conn.Close()
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("rclone socket %q is active or inaccessible; an existing rclone must accept rc/noopauth", socket)
		}
		if err := removeSocket(socket, info); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect rclone socket: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
		return nil, fmt.Errorf("create rclone socket directory: %w", err)
	}
	cmd := exec.Command("rclone", "rcd", "--rc-addr", "unix://"+socket, "--rc-no-auth", "--use-json-log")
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start rclone rcd (install rclone and make it available on PATH): %w", err)
	}
	p := &rcloneProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	ready, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return nil, fmt.Errorf("rclone rcd exited before becoming ready: %v", p.err)
		default:
		}
		if err := rcloneHealth(ready, client); err == nil {
			if err := os.Chmod(socket, 0600); err != nil {
				p.stop()
				return nil, fmt.Errorf("restrict rclone socket permissions: %w", err)
			}
			p.socket = socket
			p.socketInfo, err = os.Lstat(socket)
			if err != nil {
				p.stop()
				return nil, fmt.Errorf("inspect started rclone socket: %w", err)
			}
			logger.Info("started rclone rcd", "pid", cmd.Process.Pid, "socket", socket)
			return p, nil
		}
		select {
		case <-p.done:
			return nil, fmt.Errorf("rclone rcd exited before becoming ready: %v", p.err)
		case <-ready.Done():
			p.stop()
			return nil, fmt.Errorf("wait for rclone rcd: %w", ready.Err())
		case <-ticker.C:
		}
	}
}

// stop is called only after HTTP requests have drained. Always reap the child.
func (p *rcloneProcess) stop() {
	if p == nil {
		return
	}
	defer func() {
		if p.socketInfo != nil {
			_ = removeSocket(p.socket, p.socketInfo)
		}
	}()
	select {
	case <-p.done:
		return
	default:
	}
	if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
		_ = p.cmd.Process.Kill()
	}
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

// Only unlink the socket we inspected, never a replacement at the same path.
func removeSocket(path string, original os.FileInfo) error {
	current, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect rclone socket: %w", err)
	}
	if !os.SameFile(original, current) {
		return fmt.Errorf("rclone socket %q changed during cleanup", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale rclone socket: %w", err)
	}
	return nil
}

func rcloneHealth(ctx context.Context, client *http.Client) error {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://rclone/rc/noopauth", strings.NewReader(`{"ping":"metadonalds"}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var echo struct{ Ping string }
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("rclone health check returned HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024)).Decode(&echo); err != nil {
		return err
	}
	if echo.Ping != "metadonalds" {
		return fmt.Errorf("unexpected rclone health response")
	}
	return nil
}
