package javdb

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	tracepkg "github.com/fdkevin0/MetaDonalds/internal/trace"
)

const (
	apiCacheTTL       = 5 * time.Minute
	apiCacheEntries   = 128
	apiCacheBytes     = 16 << 20
	apiCacheBodyBytes = 4 << 20
)

var errInvalidResponse = errors.New("invalid upstream response")

func cacheErrorClass(err error) string {
	switch {
	case errors.Is(err, errInvalidResponse):
		return "invalid_response"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "upstream"
	}
}

type apiCacheEntry struct {
	data    json.RawMessage
	expires time.Time
}

type apiFlight struct {
	done    chan struct{}
	data    json.RawMessage
	err     error
	traceID string
}

type apiCache struct {
	mu      sync.Mutex
	entries map[string]apiCacheEntry
	flights map[string]*apiFlight
	bytes   int
}

type freshBrowseKey struct{}
type freshBrowseState struct {
	mu   sync.Mutex
	seen map[string]bool
}

// FreshBrowseContext makes a DAV listing refresh revalidate every source page.
// The assembled listing then owns its five-minute freshness window.
func FreshBrowseContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, freshBrowseKey{}, &freshBrowseState{seen: map[string]bool{}})
}

func freshBrowse(ctx context.Context, endpoint string) bool {
	state, ok := ctx.Value(freshBrowseKey{}).(*freshBrowseState)
	if !ok {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.seen[endpoint] {
		return false
	}
	state.seen[endpoint] = true
	return true
}

func (c *apiCache) read(key string) json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok {
		if time.Now().Before(e.expires) {
			return e.data
		}
		c.bytes -= len(e.data)
		delete(c.entries, key)
	}
	return nil
}

func (c *apiCache) store(key string, data json.RawMessage) {
	if len(data) > apiCacheBodyBytes || len(data) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]apiCacheEntry)
	}
	if old, ok := c.entries[key]; ok {
		c.bytes -= len(old.data)
		delete(c.entries, key)
	}
	for len(c.entries) >= apiCacheEntries || c.bytes+len(data) > apiCacheBytes {
		oldest := ""
		var expiry time.Time
		for k, e := range c.entries {
			if oldest == "" || e.expires.Before(expiry) {
				oldest, expiry = k, e.expires
			}
		}
		if oldest == "" {
			break
		}
		c.bytes -= len(c.entries[oldest].data)
		delete(c.entries, oldest)
	}
	c.entries[key] = apiCacheEntry{data: append(json.RawMessage(nil), data...), expires: time.Now().Add(apiCacheTTL)}
	c.bytes += len(data)
}

// fetch coalesces network reads. A canceled caller stops waiting without
// canceling another caller's request; the flight itself has a short deadline.
func (c *apiCache) fetch(ctx context.Context, key string, load func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	return c.fetchMode(ctx, key, load, false)
}

func (c *apiCache) fetchFresh(ctx context.Context, key string, load func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	return c.fetchMode(ctx, key, load, true)
}

func (c *apiCache) fetchMode(ctx context.Context, key string, load func(context.Context) (json.RawMessage, error), fresh bool, fallbackLogger ...*slog.Logger) (json.RawMessage, error) {
	ctx, waitSpan := tracepkg.Begin(ctx, "javdb.api_cache.wait")
	host, endpoint := traceEndpoint(key)
	if err := ctx.Err(); err != nil {
		waitSpan.End(tracepkg.Fields{Outcome: "error", ErrorClass: cacheErrorClass(err), Host: host, Endpoint: endpoint})
		return nil, err
	}
	cached := c.read(key)
	if cached != nil && !fresh {
		waitSpan.End(tracepkg.Fields{Outcome: "hit", Cache: "hit", Bytes: int64(len(cached)), Host: host, Endpoint: endpoint})
		return cached, nil
	}
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && time.Now().Before(e.expires) && !fresh {
		c.mu.Unlock()
		waitSpan.End(tracepkg.Fields{Outcome: "hit", Cache: "hit", Bytes: int64(len(e.data)), Host: host, Endpoint: endpoint})
		return e.data, nil
	}
	if c.flights == nil {
		c.flights = make(map[string]*apiFlight)
	}
	f := c.flights[key]
	outcome := "coalesced"
	if f == nil {
		outcome = "miss"
		if fresh && cached != nil {
			outcome = "refresh"
		}
		flightCtx, flightID := tracepkg.NewFlight(ctx, fallbackLogger...)
		f = &apiFlight{done: make(chan struct{}), traceID: flightID}
		c.flights[key] = f
		go func() {
			flightCtx, cancel := context.WithTimeout(flightCtx, 15*time.Second)
			defer cancel()
			flightCtx, fetchSpan := tracepkg.Begin(flightCtx, "javdb.api_cache.fetch")
			f.data, f.err = load(flightCtx)
			fields := tracepkg.Fields{Outcome: "ok", Cache: outcome, Host: host, Endpoint: endpoint, Bytes: int64(len(f.data))}
			if f.err != nil {
				fields.Outcome, fields.ErrorClass = "error", cacheErrorClass(f.err)
			}
			fetchSpan.End(fields)
			if f.err == nil {
				c.store(key, f.data)
			}
			c.mu.Lock()
			delete(c.flights, key)
			close(f.done)
			c.mu.Unlock()
		}()
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		waitSpan.End(tracepkg.Fields{Outcome: "error", ErrorClass: cacheErrorClass(ctx.Err()), Cache: outcome, Host: host, Endpoint: endpoint, LinkedTraceID: f.traceID})
		return nil, ctx.Err()
	case <-f.done:
		if err := ctx.Err(); err != nil {
			waitSpan.End(tracepkg.Fields{Outcome: "error", ErrorClass: cacheErrorClass(err), Cache: outcome, Host: host, Endpoint: endpoint, LinkedTraceID: f.traceID})
			return nil, err
		}
		fields := tracepkg.Fields{Outcome: "ok", Cache: outcome, Host: host, Endpoint: endpoint, LinkedTraceID: f.traceID, Bytes: int64(len(f.data))}
		if f.err != nil {
			fields.Outcome, fields.ErrorClass = "error", cacheErrorClass(f.err)
		}
		waitSpan.End(fields)
		return f.data, f.err
	}
}
