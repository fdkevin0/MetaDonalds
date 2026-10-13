// Package web opens the service in the user's browser and keeps the CLI alive.
package web

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"

	"github.com/fdkevin0/MetaDonalds/internal/client"
)

// Run leaves the browser open on cancellation; its caller owns server cleanup.
func Run(ctx context.Context, serverURL string, output io.Writer) error {
	return run(ctx, serverURL, output, startBrowser)
}

func run(ctx context.Context, serverURL string, output io.Writer, start func(string) (<-chan error, error)) error {
	u, err := client.ParseURL(serverURL)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	address := u.String() + "/"
	fmt.Fprintf(output, "MetaDonalds: %s\nPress Ctrl+C to stop this command. Closing the browser does not stop the service.\n", address)
	done, err := start(address)
	if err != nil {
		fmt.Fprintf(output, "Could not open the browser: %v. Open %s manually.\n", err, address)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-done:
			if err != nil {
				fmt.Fprintf(output, "Browser launcher failed: %v. Open %s manually.\n", err, address)
			}
			done = nil
		}
	}
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
