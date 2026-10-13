package web

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBrowserCommands(t *testing.T) {
	address := "https://example.com/"
	for _, tc := range []struct {
		os, name string
		args     []string
	}{{"darwin", "open", []string{address}}, {"linux", "xdg-open", []string{address}}, {"windows", "rundll32", []string{"url.dll,FileProtocolHandler", address}}} {
		name, args, err := browserCommand(tc.os, address)
		if err != nil || name != tc.name || !reflect.DeepEqual(args, tc.args) {
			t.Fatalf("%s: %s %v %v", tc.os, name, args, err)
		}
	}
	if _, _, err := browserCommand("other", address); err == nil {
		t.Fatal("unsupported OS accepted")
	}
}
func TestRunKeepsServiceLifetimeIndependentOfLauncher(t *testing.T) {
	for _, mode := range []string{"start-error", "exit-error", "exited", "still-running"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var output bytes.Buffer
			started := make(chan struct{})
			launcher := make(chan error, 1)
			start := func(address string) (<-chan error, error) {
				if address != "http://localhost:8080/" {
					t.Errorf("address %s", address)
				}
				close(started)
				switch mode {
				case "start-error":
					return nil, errors.New("not installed")
				case "exit-error":
					launcher <- errors.New("failed")
				case "exited":
					launcher <- nil
				}
				return launcher, nil
			}
			done := make(chan error, 1)
			go func() { done <- run(ctx, "http://localhost:8080", &output, start) }()
			<-started
			select {
			case err := <-done:
				t.Fatalf("launcher ended server lifetime: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("launcher blocked cancellation")
			}
			if !strings.Contains(output.String(), "http://localhost:8080/") {
				t.Fatal("actual URL missing")
			}
			if strings.Contains(mode, "error") && !strings.Contains(output.String(), "manually") {
				t.Fatal("manual fallback missing")
			}
		})
	}
}
func TestInvalidOrCanceledRunDoesNotOpenBrowser(t *testing.T) {
	called := false
	start := func(string) (<-chan error, error) { called = true; return nil, nil }
	var output bytes.Buffer
	if err := run(context.Background(), "http://host/path", &output, start); err == nil || called {
		t.Fatal("invalid URL reached browser")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, "http://localhost:8080", &output, start); err != nil || called {
		t.Fatal("canceled command opened browser")
	}
}
func TestStartBrowserUsesSeparateURLArgument(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("POSIX fake launcher fixture")
	}
	dir := t.TempDir()
	name, _, _ := browserCommand(runtime.GOOS, "")
	capture := filepath.Join(dir, "arguments")
	// A fake executable records argument boundaries and exits; no browser is launched.
	script := "#!/bin/sh\nprintf '%s\\n' \"$#\" \"$1\" > \"$BROWSER_TEST_CAPTURE\"\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("BROWSER_TEST_CAPTURE", capture)
	address := "http://localhost:8080/"
	done, err := startBrowser(address)
	if err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil || string(data) != "1\n"+address+"\n" {
		t.Fatalf("arguments %q %v", data, err)
	}
}
