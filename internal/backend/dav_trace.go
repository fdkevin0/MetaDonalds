package backend

import (
	"context"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DAV request diagnostics contain only fixed vocabulary and configured source IDs.
// They never retain queries, media tokens, URLs, or upstream errors.
type davTraceKey struct{}

type davTrace struct {
	mu                                           sync.Mutex
	source, section, resource, depth, item, file string
	cache, failureStage, failureCategory         string
	listItems                                    int
	listDuration                                 time.Duration
}

func davTraceFrom(ctx context.Context) *davTrace {
	trace, _ := ctx.Value(davTraceKey{}).(*davTrace)
	return trace
}

func (t *davTrace) snapshot() davTrace {
	t.mu.Lock()
	defer t.mu.Unlock()
	return davTrace{source: t.source, section: t.section, resource: t.resource, depth: t.depth, item: t.item, file: t.file, cache: t.cache, failureStage: t.failureStage, failureCategory: t.failureCategory, listItems: t.listItems, listDuration: t.listDuration}
}

func (t *davTrace) recordList(outcome string, items int, duration time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// A PROPFIND may look up the same listing twice. Keep the expensive outcome.
	priority := map[string]int{"": 0, "hit": 1, "coalesced": 2, "miss": 3, "refresh": 4}
	if priority[outcome] >= priority[t.cache] {
		t.cache, t.listItems, t.listDuration = outcome, items, duration
	}
}

func (t *davTrace) recordFailure(stage string, err error) {
	if t == nil {
		return
	}
	category := davFailureCategory(err)
	t.mu.Lock()
	t.failureStage, t.failureCategory = stage, category
	t.mu.Unlock()
}

func davFailureCategory(err error) string {
	category := "upstream"
	if errors.Is(err, context.DeadlineExceeded) {
		category = "timeout"
	} else if errors.Is(err, context.Canceled) {
		category = "canceled"
	} else if errors.Is(err, errFlatListLimit) {
		category = "limit"
	} else if errors.Is(err, errTokenCapacity) || errors.Is(err, errMediaDirectoryLimit) {
		category = "capacity"
	} else if errors.Is(err, os.ErrNotExist) {
		category = "not_found"
	} else if strings.Contains(err.Error(), "repeated") || strings.Contains(err.Error(), "cursor") {
		category = "invalid_response"
	}
	return category
}

func classifyDAVPath(r *http.Request, trace *davTrace) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	trace.resource = "directory"
	trace.section = "root"
	if r.URL.Path == "/dav" || r.URL.Path == "/dav/" {
		trace.resource = "root"
	}
	if len(parts) >= 3 {
		switch parts[2] {
		case "latest", "search", "categories", "rankings", "actors", "series", "makers":
			trace.section = parts[2]
		default:
			trace.section = "other"
		}
	}
	if len(parts) >= 4 {
		trace.resource = "listing"
		last := parts[len(parts)-1]
		switch {
		case last == "metadata.json":
			trace.resource = "metadata"
		case last == "cover.jpg" || strings.HasSuffix(last, ".jpg"):
			trace.resource = "image"
		case strings.HasSuffix(last, ".m3u8"):
			trace.resource = "playlist"
		case strings.Contains(r.URL.Path, "/media/"):
			trace.resource = "media"
		case strings.Contains(r.URL.Path, "/stream/"):
			trace.resource = "stream"
		case strings.HasPrefix(last, "000"):
			trace.resource = "movie_or_entity"
		}
	}
	itemAt := davItemIndex(parts)
	if itemAt >= 0 && len(parts) > itemAt && davLoggedItem.MatchString(parts[itemAt]) {
		trace.item = parts[itemAt]
		if len(parts) > itemAt+1 {
			trace.file = davFileLabel(parts[itemAt+1:])
		}
	}
	if r.Method == "PROPFIND" {
		switch r.Header.Get("Depth") {
		case "0", "1":
			trace.depth = r.Header.Get("Depth")
		default:
			trace.depth = "other"
		}
	}
}

var davLoggedItem = regexp.MustCompile(`^[0-9]{4}-[A-Za-z0-9_-]{1,100}$`)
var davLoggedImage = regexp.MustCompile(`^[0-9]{2}\.jpg$`)
var davLoggedVariant = regexp.MustCompile(`^video-[1-8]\.(m3u8|mp4)$`)

func davFileLabel(parts []string) string {
	switch parts[0] {
	case "metadata.json", "cover.jpg", "preview.m3u8":
		if len(parts) == 1 {
			return parts[0]
		}
	case "images":
		if len(parts) == 2 && davLoggedImage.MatchString(parts[1]) {
			return "images/" + parts[1]
		}
	case "stream":
		if len(parts) == 2 && davLoggedVariant.MatchString(parts[1]) {
			return "stream/" + parts[1]
		}
		if len(parts) == 3 && parts[1] == "media" {
			return "stream/media/{token}"
		}
	case "media":
		if len(parts) == 2 {
			return "media/{token}"
		}
	}
	return ""
}

func davItemIndex(parts []string) int {
	if len(parts) < 4 || parts[0] != "dav" {
		return -1
	}
	switch parts[2] {
	case "latest":
		return 3
	case "search":
		return 4
	case "categories":
		if len(parts) > 4 && parts[4] == "tags" {
			return 7
		}
		return 4
	case "rankings":
		if len(parts) > 3 && parts[3] == "top250" {
			return 5
		}
		return 6
	case "actors", "series", "makers":
		return 5
	}
	return -1
}
