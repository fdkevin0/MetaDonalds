// Package web opens a service URL in the user's browser.
package web

import (
	"fmt"
	"io"
	"os/exec"
	"runtime"
)

// Open starts the browser without taking ownership of its or the server's lifetime.
// Launcher failures are reported while the HTTP service continues running.
func Open(address string, output io.Writer) {
	open(address, output, startBrowser)
}

func open(address string, output io.Writer, start func(string) (<-chan error, error)) {
	fmt.Fprintf(output, "MetaDonalds: %s\nPress Ctrl+C to stop the service. Closing the browser does not stop it.\n", address)
	done, err := start(address)
	if err != nil {
		fmt.Fprintf(output, "Could not open the browser: %v. Open %s manually.\n", err, address)
		return
	}
	go func() {
		if err := <-done; err != nil {
			fmt.Fprintf(output, "Browser launcher failed: %v. Open %s manually.\n", err, address)
		}
	}()
}

func browserCommand(goos, address string) (string, []string, error) {
	switch goos {
	case "darwin":
		return "open", []string{address}, nil
	case "linux":
		return "xdg-open", []string{address}, nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", address}, nil
	default:
		return "", nil, fmt.Errorf("automatic browser opening is unsupported on %s", goos)
	}
}

func startBrowser(address string) (<-chan error, error) {
	name, args, err := browserCommand(runtime.GOOS, address)
	if err != nil {
		return nil, err
	}
	// The launcher may become the browser process. Do not tie it to server cancellation.
	cmd := exec.Command(name, args...)
	detachBrowser(cmd)
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return done, nil
}
