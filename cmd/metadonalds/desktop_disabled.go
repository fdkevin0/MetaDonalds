//go:build !desktop

package main

import "context"

const desktopAvailable = false

func runDesktop(context.Context, string, func()) error { return desktopUnavailable() }
