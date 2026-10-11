package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/backend"
)

// The test executable stands in for rclone; no installed binary or remote is used.
func TestMain(m *testing.M) {
	if os.Getenv("METADONALDS_TEST_RCLONE") == "1" && len(os.Args) > 1 && os.Args[1] == "rcd" {
		if os.Getenv("METADONALDS_TEST_RCLONE_MODE") == "exit" {
			os.Exit(2)
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		if os.Getenv("METADONALDS_TEST_RCLONE_MODE") == "wait" {
			<-ctx.Done()
			return
		}
		if len(os.Args) != 6 || os.Args[2] != "--rc-addr" || os.Args[4] != "--rc-no-auth" || os.Args[5] != "--use-json-log" {
			os.Exit(3)
		}
		listener, err := net.Listen("unix", strings.TrimPrefix(os.Args[3], "unix://"))
		if err != nil {
			os.Exit(4)
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/rc/noopauth" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.Copy(w, r.Body)
		})}
		go func() { <-ctx.Done(); srv.Close() }()
		srv.Serve(listener)
		return
	}
	os.Exit(m.Run())
}

func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "md-rc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func fakeRclone(t *testing.T) string {
	t.Helper()
	dir := shortDir(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(dir, "rclone")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("METADONALDS_TEST_RCLONE", "1")
	return filepath.Join(dir, "rclone.sock")
}

func startTestRclone(t *testing.T) *rcloneProcess {
	t.Helper()
	socket := fakeRclone(t)
	p, err := startRclone(context.Background(), socket, slog.Default(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.stop)
	info, err := os.Stat(socket)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("socket permissions: %v, %v", info, err)
	}
	return p
}

func TestRcloneStartReuseStop(t *testing.T) {
	socket := fakeRclone(t)
	p, err := startRclone(context.Background(), socket, slog.Default(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer p.stop()
	reused, err := startRclone(context.Background(), socket, slog.Default(), io.Discard)
	if err != nil || reused != nil {
		t.Fatalf("reuse: %v, %v", reused, err)
	}
	reused.stop()
	c := backend.NewRCClient(socket)
	defer c.CloseIdleConnections()
	if err := rcloneHealth(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	p.stop()
	select {
	case <-p.done:
	default:
		t.Fatal("child was not reaped")
	}
	if err := rcloneHealth(context.Background(), c); err == nil {
		t.Fatal("child still responds")
	}
}

func TestRcloneStartupFailures(t *testing.T) {
	for _, mode := range []string{"exit", "wait", "occupied", "missing"} {
		t.Run(mode, func(t *testing.T) {
			socket := fakeRclone(t)
			t.Setenv("METADONALDS_TEST_RCLONE_MODE", mode)
			if mode == "occupied" {
				if err := os.WriteFile(socket, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing" {
				t.Setenv("PATH", t.TempDir())
			}
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			p, err := startRclone(ctx, socket, slog.Default(), io.Discard)
			if err == nil || p != nil {
				t.Fatalf("expected startup failure: %v, %v", p, err)
			}
			if mode == "occupied" {
				data, _ := os.ReadFile(socket)
				if string(data) != "preserve" {
					t.Fatal("occupied path modified")
				}
			}
		})
	}
}

func TestServeStopsOnChildExit(t *testing.T) {
	p := startTestRclone(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- serve(context.Background(), listener, http.NotFoundHandler(), p) }()
	p.cmd.Process.Kill()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "exited unexpectedly") {
			t.Fatalf("child failure: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server ignored child exit")
	}
	if conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second); err == nil {
		conn.Close()
		t.Fatal("HTTP listener still open")
	}
}

func TestRcloneRecoversStaleSocket(t *testing.T) {
	socket := fakeRclone(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	listener.Close()
	p, err := startRclone(context.Background(), socket, slog.Default(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	p.stop()
	p, err = startRclone(context.Background(), socket, slog.Default(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	p.stop()
}

func TestRclonePreservesUnhealthyListener(t *testing.T) {
	socket := fakeRclone(t)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })}
	defer srv.Close()
	go srv.Serve(listener)
	before, _ := os.Lstat(socket)
	p, err := startRclone(context.Background(), socket, slog.Default(), io.Discard)
	if err == nil || p != nil {
		t.Fatalf("unhealthy listener replaced: %v", err)
	}
	after, err := os.Lstat(socket)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("listener socket changed: %v", err)
	}
}

func TestRcloneCleanupAfterCrash(t *testing.T) {
	p := startTestRclone(t)
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-p.done
	p.stop()
	if _, err := os.Lstat(p.socket); !os.IsNotExist(err) {
		t.Fatalf("crashed child socket remains: %v", err)
	}
}

func TestRcloneCleanupPreservesReplacement(t *testing.T) {
	p := startTestRclone(t)
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-p.done
	if err := os.Remove(p.socket); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.socket, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	p.stop()
	data, err := os.ReadFile(p.socket)
	if err != nil || string(data) != "replacement" {
		t.Fatalf("replacement removed: %v", err)
	}
}
