package javdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	tracepkg "github.com/fdkevin0/MetaDonalds/internal/trace"
)

func TestUntracedAPIFlightJoinedByDAV(t *testing.T) {
	var cache apiCache
	started, release := make(chan struct{}), make(chan struct{})
	firstDone := make(chan error, 1)
	load := func(context.Context) (json.RawMessage, error) {
		close(started)
		<-release
		return json.RawMessage(`{"movies":[]}`), nil
	}
	const key = "https://catalog.example/api/v2/search?q=private-query"
	var flightLogs, requestLogs bytes.Buffer
	flightLogger := slog.New(slog.NewJSONHandler(&flightLogs, nil))
	go func() {
		_, err := cache.fetchMode(context.Background(), key, load, false, flightLogger)
		firstDone <- err
	}()
	<-started
	requestLogger := slog.New(slog.NewJSONHandler(&requestLogs, nil))
	ctx, _, _ := tracepkg.NewRequest(context.Background(), requestLogger)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	_, err := cache.fetchMode(ctx, key, load, false, flightLogger)
	if err != context.DeadlineExceeded {
		t.Fatalf("coalesced caller: %v", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	var wait, fetch map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(requestLogs.Bytes()), &wait); err != nil {
		t.Fatalf("request trace: %v", err)
	}
	if err := json.Unmarshal(bytes.TrimSpace(flightLogs.Bytes()), &fetch); err != nil {
		t.Fatalf("flight trace: %v", err)
	}
	if wait["cache"] != "coalesced" || wait["linked_trace_id"] == "" || wait["linked_trace_id"] != fetch["trace_id"] || fetch["stage"] != "javdb.api_cache.fetch" {
		t.Fatalf("coalesced trace lost link: wait=%#v fetch=%#v", wait, fetch)
	}
	if bytes.Contains(requestLogs.Bytes(), []byte("private-query")) || bytes.Contains(flightLogs.Bytes(), []byte("private-query")) {
		t.Fatal("API query leaked into trace")
	}
}

func TestAPICacheHitsValidationAndIsolation(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.URL.Path]++
		n := counts[r.URL.Path]
		mu.Unlock()
		switch r.URL.Path {
		case "/v1/movies/latest":
			fmt.Fprint(w, `{"success":1,"data":{"movies":[{"id":"abc","number":"EX-1"}]}}`)
		case "/v4/movies/abc":
			fmt.Fprint(w, `{"success":1,"data":{"movie":{"id":"abc","number":"EX-1"}}}`)
		case "/v2/search":
			fmt.Fprint(w, `{"success":1,"data":{}}`)
		case "/v1/rankings/playback":
			if n == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			fmt.Fprint(w, `{"success":1,"data":{"movies":[]}}`)
		case "/top250/all.json":
			fmt.Fprint(w, `{"movies":[{"id":"abc","number":"EX-1"}]}`)
		}
	}))
	defer srv.Close()
	s := &Source{ID: "one", BaseURL: srv.URL, Top250BaseURL: srv.URL + "/top250", Client: srv.Client()}
	for range 2 {
		if _, err := s.Latest(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Detail(context.Background(), "abc"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Media(context.Background(), "abc"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RankingMovies(context.Background(), "top250", "all", "", 1); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := s.Search(context.Background(), "bad", ""); err == nil {
			t.Fatal("invalid response accepted")
		}
	}
	if _, err := s.RankingMovies(context.Background(), "playback", "all", "daily", 1); err == nil {
		t.Fatal("HTTP error accepted")
	}
	if _, err := s.RankingMovies(context.Background(), "playback", "all", "daily", 1); err != nil {
		t.Fatal(err)
	}
	other := &Source{ID: "two", BaseURL: srv.URL, Client: srv.Client()}
	if _, err := other.Latest(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for path, want := range map[string]int{"/v1/movies/latest": 2, "/v4/movies/abc": 1, "/v2/search": 2, "/v1/rankings/playback": 2, "/top250/all.json": 1} {
		if counts[path] != want {
			t.Errorf("%s requests: got %d, want %d", path, counts[path], want)
		}
	}
}

func TestAPICacheConcurrentAndCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		if requests == 1 {
			close(started)
		}
		mu.Unlock()
		<-release
		fmt.Fprint(w, `{"success":1,"data":{"movies":[]}}`)
	}))
	defer srv.Close()
	s := &Source{BaseURL: srv.URL, Client: srv.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := s.Latest(ctx, 1); first <- err }()
	<-started
	const waiters = 12
	var wg sync.WaitGroup
	for range waiters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Latest(context.Background(), 1); err != nil {
				t.Error(err)
			}
		}()
	}
	cancel()
	if err := <-first; err != context.Canceled {
		t.Fatalf("canceled caller: %v", err)
	}
	close(release)
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("coalesced requests: %d", requests)
	}
}

func TestAPICacheExpiryAndBounds(t *testing.T) {
	var c apiCache
	c.store("expired", json.RawMessage(`{"a":1}`))
	c.mu.Lock()
	e := c.entries["expired"]
	e.expires = time.Now().Add(-time.Second)
	c.entries["expired"] = e
	c.mu.Unlock()
	if c.read("expired") != nil {
		t.Fatal("expired response returned")
	}
	for i := range apiCacheEntries + 20 {
		c.store(fmt.Sprintf("key-%d", i), json.RawMessage(`{"a":1}`))
	}
	if len(c.entries) > apiCacheEntries || c.bytes > apiCacheBytes {
		t.Fatalf("unbounded cache: %d entries, %d bytes", len(c.entries), c.bytes)
	}
	c.store("key-147", json.RawMessage(`{"replacement":"larger"}`))
	actual := 0
	for _, e := range c.entries {
		actual += len(e.data)
	}
	if c.bytes != actual {
		t.Fatalf("replacement byte count: got %d, actual %d", c.bytes, actual)
	}
	oversize := make(json.RawMessage, apiCacheBodyBytes+1)
	c.store("oversize", oversize)
	if c.read("oversize") != nil {
		t.Fatal("oversize response cached")
	}
}

func TestDetailDoesNotCacheMediaInvalidPayload(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		n := requests
		mu.Unlock()
		if n == 1 {
			fmt.Fprint(w, `{"success":1,"data":{"movie":{"id":"abc","cover_url":123}}}`)
			return
		}
		fmt.Fprint(w, `{"success":1,"data":{"movie":{"id":"abc","cover_url":"https://example.test/cover.jpg"}}}`)
	}))
	defer srv.Close()
	s := &Source{BaseURL: srv.URL, Client: srv.Client()}
	if _, err := s.Detail(context.Background(), "abc"); err == nil {
		t.Fatal("malformed media detail accepted")
	}
	m, err := s.Media(context.Background(), "abc")
	if err != nil || m.CoverURL != "https://example.test/cover.jpg" {
		t.Fatalf("corrected media response: %+v %v", m, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Fatalf("invalid response was cached: %d requests", requests)
	}
}
