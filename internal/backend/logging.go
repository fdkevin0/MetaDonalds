package backend

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	tracepkg "github.com/fdkevin0/MetaDonalds/internal/trace"
	"github.com/felixge/httpsnoop"
)

func logRequests(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var trace *davTrace
		if r.URL.Path == "/dav" || len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/dav/" {
			trace = &davTrace{}
			classifyDAVPath(r, trace)
			ctx, traceID, _ := tracepkg.NewRequest(r.Context(), logger)
			w.Header().Set("X-Trace-ID", traceID)
			r = r.WithContext(context.WithValue(ctx, davTraceKey{}, trace))
		}
		metrics := httpsnoop.CaptureMetrics(next, w, r)
		level := slog.LevelInfo
		if metrics.Code >= 500 {
			level = slog.LevelError
		} else if metrics.Code >= 400 {
			level = slog.LevelWarn
		}
		// Log the matched route, not user-controlled URLs, queries, or bodies.
		attrs := []any{"method", r.Method, "route", r.Pattern, "status", metrics.Code,
			"bytes", metrics.Written, "duration_ms", float64(metrics.Duration) / float64(time.Millisecond)}
		if trace != nil {
			traceID, spanID := tracepkg.IDs(r.Context())
			attrs = append(attrs, "trace_id", traceID, "span_id", spanID)
			v := trace.snapshot()
			attrs = append(attrs, "dav_section", v.section, "dav_resource", v.resource)
			if v.source != "" {
				attrs = append(attrs, "dav_source", v.source)
			}
			if v.item != "" {
				attrs = append(attrs, "dav_item", v.item)
			}
			if v.file != "" {
				attrs = append(attrs, "dav_file", v.file)
			}
			if v.depth != "" {
				attrs = append(attrs, "dav_depth", v.depth)
			}
			if v.cache != "" {
				attrs = append(attrs, "dav_listing_cache", v.cache, "dav_list_items", v.listItems,
					"dav_list_ms", float64(v.listDuration)/float64(time.Millisecond))
			}
			if v.failureStage != "" {
				attrs = append(attrs, "dav_failure_stage", v.failureStage, "dav_failure_category", v.failureCategory)
			}
		}
		logger.Log(r.Context(), level, "HTTP request", attrs...)
	})
}
