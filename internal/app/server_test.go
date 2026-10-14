package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/fdkevin0/MetaDonalds/internal/backend"
	"testing"
	"time"
)

func TestServeDrainsRequests(t *testing.T) {
	p := startTestRclone(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-release
			io.WriteString(w, "finished")
		}), p)
	}()
	response := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			defer resp.Body.Close()
			var body []byte
			body, err = io.ReadAll(resp.Body)
			if err == nil && string(body) != "finished" {
				err = io.ErrUnexpectedEOF
			}
		}
		response <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("server exited before draining: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-p.done:
		t.Fatal("rclone stopped before HTTP requests drained")
	default:
	}
	release <- struct{}{}
	select {
	case err := <-response:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request did not finish")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop")
	}
}

func TestServeCallsOpenerWhenHTTPIsReady(t *testing.T) {
	for _, listen := range []string{"127.0.0.1:0", "0.0.0.0:0", "[::1]:0", "[::]:0", ":0"} {
		t.Run(listen, func(t *testing.T) {
			if strings.Contains(listen, "[") {
				probe, err := net.Listen("tcp", listen)
				if err != nil {
					t.Skipf("IPv6 unavailable: %v", err)
				}
				probe.Close()
			}
			cfg := backend.Config{Plugins: &backend.PluginConfig{}}
			cfg.Server.ListenAddr = listen
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			called := false
			err := Serve(ctx, cfg, func(address string) {
				called = true
				defer cancel()
				u, err := url.Parse(address)
				if err != nil {
					t.Error(err)
					return
				}
				if u.Port() == "" || u.Port() == "0" || !net.ParseIP(u.Hostname()).IsLoopback() {
					t.Errorf("not a usable bound address: %s", address)
				}
				if listen == "0.0.0.0:0" && u.Hostname() != "127.0.0.1" {
					t.Errorf("IPv4 wildcard opened %s", address)
				}
				if listen == "[::]:0" && u.Hostname() != "::1" {
					t.Errorf("IPv6 wildcard opened %s", address)
				}
				// This acts as the opener: HTTP must be usable at the instant it is called.
				if err := health(ctx, strings.TrimSuffix(address, "/")); err != nil {
					t.Errorf("opened before ready: %v", err)
				}
			})
			if err != nil || !called {
				t.Fatalf("Serve: %v, opened=%v", err, called)
			}
		})
	}
}

func TestBrowserURLPreservesBoundAddressAndPort(t *testing.T) {
	for _, tc := range []struct{ listen, bound, want string }{
		{"0.0.0.0:0", "[::]:4321", "http://127.0.0.1:4321"},
		{"[::]:0", "[::]:4321", "http://[::1]:4321"},
		{":0", "0.0.0.0:4321", "http://127.0.0.1:4321"},
		{":0", "[::]:4321", "http://[::1]:4321"},
		{"192.168.1.23:0", "192.168.1.23:4321", "http://192.168.1.23:4321"},
		{"[2001:db8::7]:8080", "[2001:db8::7]:8080", "http://[2001:db8::7]:8080"},
		{"localhost:0", "127.0.0.1:4321", "http://127.0.0.1:4321"},
		{"[fe80::7%eth0]:0", "[fe80::7%eth0]:4321", "http://[fe80::7%25eth0]:4321"},
	} {
		bound, err := net.ResolveTCPAddr("tcp", tc.bound)
		if err != nil {
			t.Fatal(err)
		}
		if got := browserURL(tc.listen, bound); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.listen, got, tc.want)
		}
	}
}

func TestCanceledServeDoesNotOpenBrowser(t *testing.T) {
	cfg := backend.Config{Plugins: &backend.PluginConfig{}}
	cfg.Server.ListenAddr = "127.0.0.1:0"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = Serve(ctx, cfg, func(string) { t.Error("canceled serve opened browser") })
}
