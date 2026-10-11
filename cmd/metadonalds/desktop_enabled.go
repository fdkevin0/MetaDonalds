//go:build desktop

package main

import (
	"context"

	"github.com/fdkevin0/MetaDonalds/internal/desktop"
)

const desktopAvailable = true

func runDesktop(ctx context.Context, serverURL string, stop func()) error {
	return desktop.Run(ctx, serverURL, stop)
}
