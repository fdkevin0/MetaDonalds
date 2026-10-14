// Package trace adds request-local, structured diagnostics to the DAV pipeline.
// Stages accept only caller-selected fields; URLs, headers, and errors are never
// formatted into log messages here.
package trace

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type key struct{}

type state struct {
	traceID, spanID string
	logger          *slog.Logger
}

type Span struct {
	state
	parentID, stage string
	started         time.Time
	once            sync.Once
}

type Fields struct {
	Outcome, ErrorClass, Cache, Host, Endpoint, Method, LinkedTraceID string
	Status                                                            int
	Bytes                                                             int64
	Count                                                             int
}

var fallbackCounter atomic.Uint64

func newID(bytes int) string {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		// A missing system random source must not break DAV requests.
		var seed [16]byte
		binary.BigEndian.PutUint64(seed[:8], uint64(time.Now().UnixNano()))
		binary.BigEndian.PutUint64(seed[8:], fallbackCounter.Add(1))
		sum := sha256.Sum256(seed[:])
		copy(b, sum[:])
	}
	return hex.EncodeToString(b)
}

func NewRequest(ctx context.Context, logger *slog.Logger) (context.Context, string, string) {
	if logger == nil {
		logger = slog.Default()
	}
	s := state{traceID: newID(16), spanID: newID(8), logger: logger}
	return context.WithValue(ctx, key{}, s), s.traceID, s.spanID
}

// NewFlight creates a separate trace for work that may outlive its first waiter.
// The waiter logs linked_trace_id; the flight never writes into that request's
// trace state after it has completed.
func NewFlight(ctx context.Context, fallback ...*slog.Logger) (context.Context, string) {
	s, ok := ctx.Value(key{}).(state)
	if !ok {
		logger := slog.Default()
		if len(fallback) > 0 && fallback[0] != nil {
			logger = fallback[0]
		}
		s = state{traceID: newID(16), logger: logger}
	} else {
		// The first stage logged by the flight is its root span.
		s = state{traceID: newID(16), logger: s.logger}
	}
	return context.WithValue(context.WithoutCancel(ctx), key{}, s), s.traceID
}

func IDs(ctx context.Context) (traceID, spanID string) {
	if s, ok := ctx.Value(key{}).(state); ok {
		return s.traceID, s.spanID
	}
	return "", ""
}

func Begin(ctx context.Context, stage string) (context.Context, *Span) {
	s, ok := ctx.Value(key{}).(state)
	if !ok {
		return ctx, nil
	}
	child := &Span{state: state{traceID: s.traceID, spanID: newID(8), logger: s.logger}, parentID: s.spanID, stage: stage, started: time.Now()}
	return context.WithValue(ctx, key{}, child.state), child
}

func (s *Span) End(f Fields) {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if f.Outcome == "" {
			f.Outcome = "ok"
		}
		attrs := []slog.Attr{
			slog.String("trace_id", s.traceID), slog.String("span_id", s.spanID),
			slog.String("parent_span_id", s.parentID), slog.String("stage", s.stage),
			slog.String("outcome", f.Outcome),
			slog.Float64("duration_ms", float64(time.Since(s.started))/float64(time.Millisecond)),
			slog.Int64("bytes", f.Bytes), slog.Int("count", f.Count),
		}
		if f.Status != 0 {
			attrs = append(attrs, slog.Int("status", f.Status))
		}
		if f.ErrorClass != "" {
			attrs = append(attrs, slog.String("error_class", f.ErrorClass))
		}
		if f.Cache != "" {
			attrs = append(attrs, slog.String("cache", f.Cache))
		}
		if f.Host != "" {
			attrs = append(attrs, slog.String("upstream_host", f.Host))
		}
		if f.Endpoint != "" {
			attrs = append(attrs, slog.String("endpoint", f.Endpoint))
		}
		if f.Method != "" {
			attrs = append(attrs, slog.String("method", f.Method))
		}
		if f.LinkedTraceID != "" {
			attrs = append(attrs, slog.String("linked_trace_id", f.LinkedTraceID))
		}
		level := slog.LevelInfo
		if f.ErrorClass != "" || f.Status >= 500 {
			level = slog.LevelError
		} else if f.Status >= 400 {
			level = slog.LevelWarn
		}
		s.logger.LogAttrs(context.Background(), level, "DAV stage", attrs...)
	})
}
