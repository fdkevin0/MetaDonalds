package main

import (
	"log/slog"
	"net/http"
	"time"
)

type loggedResponse struct {
	http.ResponseWriter
	status int
}

func (w *loggedResponse) WriteHeader(status int) {
	if w.status == 0 && status >= 200 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggedResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *loggedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func logRequests(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		response := &loggedResponse{ResponseWriter: w}
		next.ServeHTTP(response, r)
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		} else if status >= 400 {
			level = slog.LevelWarn
		}
		// Log the matched route, not user-controlled URLs, queries, or bodies.
		logger.Log(r.Context(), level, "HTTP request", "method", r.Method,
			"route", r.Pattern, "status", status,
			"duration_ms", float64(time.Since(start).Microseconds())/1000)
	})
}
