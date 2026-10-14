package web

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
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

type lockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}
func TestOpenReturnsWithoutWaitingForBrowser(t *testing.T) {
	for _, mode := range []string{"start-error", "exit-error", "exited", "still-running"} {
		t.Run(mode, func(t *testing.T) {
			var output lockedBuffer
			launcher := make(chan error, 1)
			defer close(launcher)
			start := func(address string) (<-chan error, error) {
				if address != "http://localhost:8080/" {
					t.Errorf("address %s", address)
				}
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
			done := make(chan struct{})
			go func() { open("http://localhost:8080/", &output, start); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("browser blocked server startup")
			}
			if !strings.Contains(output.String(), "http://localhost:8080/") {
				t.Fatal("actual URL missing")
			}
			if strings.Contains(mode, "error") {
				deadline := time.Now().Add(time.Second)
				for !strings.Contains(output.String(), "manually") && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if !strings.Contains(output.String(), "manually") {
					t.Fatal("manual fallback missing")
				}
			}
		})
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
