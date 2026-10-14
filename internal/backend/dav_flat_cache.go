package backend

import (
	"context"
	"errors"
	"log/slog"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/core"
	"github.com/fdkevin0/MetaDonalds/internal/plugins/javdb"
	tracepkg "github.com/fdkevin0/MetaDonalds/internal/trace"
)

const (
	flatTTL       = 5 * time.Minute
	flatMaxAge    = 2 * time.Hour
	flatActiveFor = 30 * time.Minute
	flatTick      = 30 * time.Second
	flatMaxLists  = 32
	flatMaxBytes  = 16 << 20
	flatMaxItems  = 9999
	flatFetchTime = 60 * time.Second
)

var errFlatListLimit = errors.New("DAV listing exceeds the in-memory limit")

type flatSnapshot struct {
	nodes     []davNode
	items     map[string]core.Item
	entities  map[string]javdb.BrowseEntity
	tags      []javdb.BrowseTagGroup
	fetchedAt time.Time
	expiresAt time.Time
	bytes     int
}

type flatFlight struct {
	done    chan struct{}
	data    *flatSnapshot
	err     error
	traceID string
}

type flatActivity struct {
	accessed time.Time
	retryAt  time.Time
	failures int
}

type flatRefreshKey struct{}

type flatCache struct {
	mu       sync.Mutex
	entries  map[string]*flatSnapshot
	flights  map[string]*flatFlight
	bytes    int
	activity map[string]flatActivity
	lifetime context.Context
	cancel   context.CancelFunc
	closed   bool
	wg       sync.WaitGroup
	disk     *flatDisk
	logger   *slog.Logger
}

// modTime reports an already-loaded directory snapshot without triggering a
// network fetch for every child of a parent PROPFIND.
func (c *flatCache) modTime(ctx context.Context, name string) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, prefix := range []string{"movies:", "entities:", "tags:"} {
		key := prefix + path.Clean(name)
		if memo, ok := ctx.Value(davMemoKey{}).(*davMemo); ok {
			if entry := memo.snapshots[key]; entry != nil {
				return entry.fetchedAt
			}
		}
		if entry := c.entries[key]; entry != nil && time.Now().Before(entry.fetchedAt.Add(flatMaxAge)) {
			if memo, ok := ctx.Value(davMemoKey{}).(*davMemo); ok {
				if memo.snapshots == nil {
					memo.snapshots = map[string]*flatSnapshot{}
				}
				memo.snapshots[key] = entry
			}
			return entry.fetchedAt
		}
	}
	return time.Time{}
}

func (c *flatCache) load(ctx context.Context, key string, fetch func(context.Context) (*flatSnapshot, error)) (*flatSnapshot, error) {
	started := time.Now()
	trace := davTraceFrom(ctx)
	ctx, waitSpan := tracepkg.Begin(ctx, "dav.list.wait")
	if err := ctx.Err(); err != nil {
		waitSpan.End(tracepkg.Fields{Outcome: "error", ErrorClass: "canceled"})
		return nil, err
	}
	background, _ := ctx.Value(flatRefreshKey{}).(bool)
	if !background {
		if memo, ok := ctx.Value(davMemoKey{}).(*davMemo); ok {
			if entry := memo.snapshots[key]; entry != nil {
				waitSpan.End(tracepkg.Fields{Outcome: "hit", Cache: "hit", Count: len(entry.nodes)})
				return entry, nil
			}
		}
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, context.Canceled
	}
	if c.activity == nil {
		c.activity = map[string]flatActivity{}
	}
	activity := c.activity[key]
	if !background {
		activity.accessed = time.Now()
		c.activity[key] = activity
	}
	entry := c.entries[key]
	usable := entry != nil && time.Now().Before(entry.fetchedAt.Add(flatMaxAge))
	outcome := "hit"
	var flight *flatFlight
	if background || !usable || !time.Now().Before(entry.expiresAt) {
		outcome = "miss"
		if entry != nil {
			outcome = "refresh"
		}
		flight = c.flights[key]
		if flight != nil {
			outcome = "coalesced"
		}
		if flight == nil && (!usable && !background || len(c.flights) == 0 && !time.Now().Before(activity.retryAt)) {
			if len(c.flights) < flatMaxLists {
				flight = c.startLocked(ctx, key, fetch, outcome)
			} else if !usable {
				if entry == nil {
					delete(c.activity, key)
				}
				c.mu.Unlock()
				return nil, errFlatListLimit
			}
		}
		if usable && !background {
			outcome = "stale"
		}
	}
	if usable && !background {
		c.mu.Unlock()
		if memo, ok := ctx.Value(davMemoKey{}).(*davMemo); ok {
			if memo.snapshots == nil {
				memo.snapshots = map[string]*flatSnapshot{}
			}
			memo.snapshots[key] = entry
		}
		trace.recordList(outcome, len(entry.nodes), time.Since(started))
		waitSpan.End(tracepkg.Fields{Outcome: outcome, Cache: outcome, Count: len(entry.nodes)})
		return entry, nil
	}
	if flight == nil {
		c.mu.Unlock()
		return entry, nil
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		waitSpan.End(tracepkg.Fields{Outcome: "error", Cache: outcome, LinkedTraceID: flight.traceID, ErrorClass: "canceled"})
		return nil, ctx.Err()
	case <-flight.done:
		if err := ctx.Err(); err != nil {
			waitSpan.End(tracepkg.Fields{Outcome: "error", Cache: outcome, LinkedTraceID: flight.traceID, ErrorClass: "canceled"})
			return nil, err
		}
		if flight.err != nil {
			trace.recordList(outcome, 0, time.Since(started))
			stage := "listing"
			if cut := strings.IndexByte(key, ':'); cut > 0 {
				stage = key[:cut]
			}
			trace.recordFailure(stage, flight.err)
			waitSpan.End(tracepkg.Fields{Outcome: "error", Cache: outcome, LinkedTraceID: flight.traceID, ErrorClass: davFailureCategory(flight.err)})
			return nil, flight.err
		}
		trace.recordList(outcome, len(flight.data.nodes), time.Since(started))
		waitSpan.End(tracepkg.Fields{Outcome: "ok", Cache: outcome, LinkedTraceID: flight.traceID, Count: len(flight.data.nodes), Bytes: int64(flight.data.bytes)})
		if memo, ok := ctx.Value(davMemoKey{}).(*davMemo); ok && !background {
			if memo.snapshots == nil {
				memo.snapshots = map[string]*flatSnapshot{}
			}
			memo.snapshots[key] = flight.data
		}
		return flight.data, flight.err
	}
}

// startLocked publishes only complete snapshots; the flight also owns its disk write.
func (c *flatCache) startLocked(ctx context.Context, key string, fetch func(context.Context) (*flatSnapshot, error), outcome string) *flatFlight {
	if c.lifetime == nil {
		c.lifetime, c.cancel = context.WithCancel(context.Background())
	}
	if c.flights == nil {
		c.flights = map[string]*flatFlight{}
	}
	flightCtx, id := tracepkg.NewFlight(ctx, c.logger)
	flight := &flatFlight{done: make(chan struct{}), traceID: id}
	c.flights[key] = flight
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		workCtx, cancel := context.WithTimeout(flightCtx, flatFetchTime)
		stop := context.AfterFunc(c.lifetime, cancel)
		defer stop()
		defer cancel()
		workCtx, span := tracepkg.Begin(workCtx, "dav.list.fetch")
		started := time.Now().UTC()
		entry, err := fetch(workCtx)
		if err == nil {
			err = workCtx.Err()
		}
		if err == nil {
			if entry == nil || len(entry.nodes) > flatMaxItems || entry.bytes > flatMaxBytes {
				err = errFlatListLimit
			} else {
				entry.fetchedAt = started
				entry.expiresAt = started.Add(flatTTL)
				for i := range entry.nodes {
					if entry.nodes[i].modTime.IsZero() {
						entry.nodes[i].modTime = started
					}
				}
			}
		}
		fields := tracepkg.Fields{Outcome: "ok", Cache: outcome}
		if err != nil {
			fields.Outcome, fields.ErrorClass = "error", davFailureCategory(err)
		} else {
			fields.Count, fields.Bytes = len(entry.nodes), int64(entry.bytes)
		}
		span.End(fields)
		c.mu.Lock()
		activity := c.activity[key]
		if c.closed {
			err = context.Canceled
		}
		if err == nil {
			c.storeLocked(key, entry)
			activity.failures, activity.retryAt = 0, time.Time{}
		} else {
			activity.failures = min(activity.failures+1, 6)
			activity.retryAt = time.Now().Add(min(30*time.Second*time.Duration(1<<(activity.failures-1)), 15*time.Minute))
		}
		if c.entries[key] != nil {
			c.activity[key] = activity
		} else {
			delete(c.activity, key)
		}
		disk := c.disk
		c.mu.Unlock()
		if err == nil && disk != nil {
			if writeErr := disk.write(key, entry, activity.accessed); writeErr != nil {
				c.logger.Warn("DAV cache persistence failed", "operation", "write", "error", writeErr)
			}
		}
		c.mu.Lock()
		flight.data, flight.err = entry, err
		delete(c.flights, key)
		close(flight.done)
		c.mu.Unlock()
	}()
	return flight
}

func (c *flatCache) storeLocked(key string, entry *flatSnapshot) {
	if c.entries == nil {
		c.entries = map[string]*flatSnapshot{}
	}
	if old := c.entries[key]; old != nil {
		c.bytes -= old.bytes
		delete(c.entries, key)
	}
	for len(c.entries) >= flatMaxLists || c.bytes+entry.bytes > flatMaxBytes {
		oldestKey := ""
		var oldest time.Time
		for name, candidate := range c.entries {
			if oldestKey == "" || candidate.fetchedAt.Before(oldest) {
				oldestKey, oldest = name, candidate.fetchedAt
			}
		}
		if oldestKey == "" {
			break
		}
		c.bytes -= c.entries[oldestKey].bytes
		delete(c.entries, oldestKey)
		delete(c.activity, oldestKey)
	}
	c.entries[key] = entry
	c.bytes += entry.bytes
}

func (c *flatCache) close() {
	c.mu.Lock()
	c.closed = true
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
	c.wg.Wait()
	if c.disk != nil {
		c.disk.close()
	}
}

func (c *flatCache) due(now time.Time) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || len(c.flights) != 0 {
		return ""
	}
	key := ""
	var oldest time.Time
	for name, entry := range c.entries {
		a := c.activity[name]
		if now.Sub(a.accessed) > flatActiveFor || now.Before(entry.expiresAt) || now.Before(a.retryAt) {
			continue
		}
		if key == "" || entry.expiresAt.Before(oldest) {
			key, oldest = name, entry.expiresAt
		}
	}
	return key
}
