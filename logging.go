package main

import (
	"log/slog"
	"net/http"

	"github.com/felixge/httpsnoop"
)

func logRequests(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics := httpsnoop.CaptureMetrics(next, w, r)
		level := slog.LevelInfo
		if metrics.Code >= 500 {
			level = slog.LevelError
		} else if metrics.Code >= 400 {
			level = slog.LevelWarn
		}
		// Log the matched route, not user-controlled URLs, queries, or bodies.
		logger.Log(r.Context(), level, "HTTP request", "method", r.Method,
			"route", r.Pattern, "status", metrics.Code,
			"duration_ms", float64(metrics.Duration.Microseconds())/1000)
	})
}
