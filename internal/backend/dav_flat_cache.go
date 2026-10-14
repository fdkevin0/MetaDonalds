package backend

import (
	"context"
	"errors"
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

type flatCache struct {
	mu      sync.Mutex
	entries map[string]*flatSnapshot
	flights map[string]*flatFlight
	bytes   int
}

// modTime reports an already-loaded directory snapshot without triggering a
// network fetch for every child of a parent PROPFIND.
func (c *flatCache) modTime(name string) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, prefix := range []string{"movies:", "entities:", "tags:"} {
		if entry := c.entries[prefix+path.Clean(name)]; entry != nil && time.Now().Before(entry.expiresAt) {
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
	c.mu.Lock()
	if entry := c.entries[key]; entry != nil && time.Now().Before(entry.expiresAt) {
		c.mu.Unlock()
		trace.recordList("hit", len(entry.nodes), time.Since(started))
		waitSpan.End(tracepkg.Fields{Outcome: "hit", Cache: "hit", Count: len(entry.nodes)})
		return entry, nil
	}
	wasExpired := c.entries[key] != nil
	if c.flights == nil {
		c.flights = map[string]*flatFlight{}
	}
	flight := c.flights[key]
	outcome := "coalesced"
	if flight == nil {
		outcome = "miss"
		if wasExpired {
			outcome = "refresh"
		}
		flightCtx, flightID := tracepkg.NewFlight(ctx)
		flight = &flatFlight{done: make(chan struct{}), traceID: flightID}
		c.flights[key] = flight
		go func() {
			started := time.Now().UTC()
			workCtx, cancel := context.WithTimeout(flightCtx, flatFetchTime)
			defer cancel()
			workCtx, fetchSpan := tracepkg.Begin(workCtx, "dav.list.fetch")
			entry, err := fetch(workCtx)
			if err == nil {
				entry.fetchedAt = started
				entry.expiresAt = started.Add(flatTTL)
				if len(entry.nodes) > flatMaxItems || entry.bytes > flatMaxBytes {
					err = errFlatListLimit
				} else {
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
			fetchSpan.End(fields)
			c.mu.Lock()
			if err == nil {
				if c.entries == nil {
					c.entries = map[string]*flatSnapshot{}
				}
				if old := c.entries[key]; old != nil {
					c.bytes -= old.bytes
					delete(c.entries, key)
				}
				for len(c.entries) >= flatMaxLists || c.bytes+entry.bytes > flatMaxBytes {
					var oldestKey string
					var oldest time.Time
					for name, candidate := range c.entries {
						if oldestKey == "" || candidate.expiresAt.Before(oldest) {
							oldestKey, oldest = name, candidate.expiresAt
						}
					}
					if oldestKey == "" {
						break
					}
					c.bytes -= c.entries[oldestKey].bytes
					delete(c.entries, oldestKey)
				}
				c.entries[key] = entry
				c.bytes += entry.bytes
			}
			flight.data, flight.err = entry, err
			delete(c.flights, key)
			close(flight.done)
			c.mu.Unlock()
		}()
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
		return flight.data, flight.err
	}
}
