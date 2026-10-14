package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type partialErrorBody struct{ remaining int }

func (b *partialErrorBody) Read(p []byte) (int, error) {
	if b.remaining > 0 {
		n := min(len(p), b.remaining)
		for i := range p[:n] {
			p[i] = 'x'
		}
		b.remaining -= n
		return n, nil
	}
	return 0, io.ErrUnexpectedEOF
}

func (b *partialErrorBody) Close() error { return nil }

func TestDAVProxyCopyFailureAbortsRealResponse(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	d := &davServer{client: &http.Client{Transport: davRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"video/mp4"}}, ContentLength: -1, Body: &partialErrorBody{remaining: 64 << 10}, Request: r}, nil
	})}}
	srv := httptest.NewServer(logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Pattern = "/dav/{path...}"
		d.proxyMedia(w, r, "https://media.example/video.mp4")
	}), logger))
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL + "/dav/video.mp4")
	var data []byte
	readErr := err
	if err == nil {
		data, readErr = io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	if err != nil || resp.StatusCode != http.StatusOK || len(data) < 4096 {
		t.Fatalf("expected a partial HTTP 200 body before abort: status=%v bytes=%d err=%v", resp, len(data), err)
	}
	if readErr == nil {
		t.Fatalf("partial upstream body completed as clean HTTP %d with %q", resp.StatusCode, data)
	}
	entries := traceEntries(t, output.Bytes())
	var copyFailed, aborted bool
	for _, entry := range entries {
		if entry["stage"] == "dav.proxy.copy" && entry["outcome"] == "error" && entry["error_class"] == "copy" {
			copyFailed = true
		}
		if entry["msg"] == "HTTP request" && entry["outcome"] == "aborted" && entry["error_class"] == "response_aborted" && entry["trace_id"] != "" {
			aborted = true
		}
	}
	if !copyFailed || !aborted {
		t.Fatalf("copy abort missing from trace: %#v", entries)
	}
}

func TestDAVProxyBodyIdleTimeout(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		stalls     bool
	}{
		{"headers_then_stall", "/headers", true},
		{"data_then_stall", "/partial", true},
		{"progress_exceeds_timeout", "/progress", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canceled := make(chan struct{}, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "video/mp4")
				w.WriteHeader(http.StatusOK)
				if tc.path == "/partial" {
					_, _ = w.Write([]byte("part"))
				}
				w.(http.Flusher).Flush()
				if tc.stalls {
					<-r.Context().Done()
					canceled <- struct{}{}
					return
				}
				for range 12 {
					_, _ = w.Write([]byte("x"))
					w.(http.Flusher).Flush()
					time.Sleep(15 * time.Millisecond)
				}
			}))
			defer upstream.Close()
			transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", upstream.Listener.Addr().String())
			}}
			defer transport.CloseIdleConnections()
			d := &davServer{client: &http.Client{Transport: transport}, mediaIdleTimeout: 40 * time.Millisecond}
			downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				d.proxyMedia(w, r, "http://media.example"+tc.path)
			}))
			defer downstream.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
			defer cancel()
			start := time.Now()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, downstream.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := downstream.Client().Do(req)
			var body []byte
			if err == nil {
				body, err = io.ReadAll(resp.Body)
				resp.Body.Close()
			}
			elapsed := time.Since(start)
			if tc.stalls {
				if err == nil || elapsed > 200*time.Millisecond {
					t.Fatalf("stalled body did not fail promptly: elapsed=%s body=%q err=%v", elapsed, body, err)
				}
				select {
				case <-canceled:
				case <-time.After(200 * time.Millisecond):
					t.Fatal("stalled upstream request was not canceled")
				}
			} else if err != nil || len(body) != 12 || elapsed < 120*time.Millisecond {
				t.Fatalf("progressing body failed despite continuous data: elapsed=%s body=%q err=%v", elapsed, body, err)
			}
		})
	}
}

func TestOperationLogs(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, failed := range []bool{false, true} {
		for _, operation := range []string{"search", "submission"} {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			var upstreamErr error
			if failed {
				upstreamErr = errors.New("private-upstream-token")
			}
			s := server{
				search: func(context.Context, NyaaConfig, string) ([]torrent, error) {
					return []torrent{{InfoHash: testHash}}, upstreamErr
				},
				submit: func(context.Context, string) error { return upstreamErr },
			}
			req := httptest.NewRequest("GET", "/api/v1/search?q=+Frieren+", nil)
			field, value, outcome := "query", "Frieren", "completed"
			if operation == "submission" {
				req = httptest.NewRequest("POST", "/api/v1/submit", strings.NewReader(`{"info_hash":"`+strings.ToUpper(testHash)+`"}`))
				req.Header.Set("Content-Type", "application/json")
				field, value, outcome = "info_hash", testHash, "accepted"
			}
			if failed {
				outcome = "failed"
			}
			response := httptest.NewRecorder()
			s.routes().ServeHTTP(response, req)
			if strings.Contains(output.String(), "private-upstream-token") {
				t.Fatal("upstream error leaked into logs")
			}
			wantStatus := http.StatusOK
			if failed {
				wantStatus = http.StatusBadGateway
			}
			if response.Code != wantStatus {
				t.Fatalf("unexpected HTTP status: %d", response.Code)
			}
			decoder := json.NewDecoder(&output)
			for _, phase := range []string{"started", outcome} {
				var entry map[string]any
				if err := decoder.Decode(&entry); err != nil {
					t.Fatal(err)
				}
				level := "INFO"
				if phase == "failed" {
					level = "ERROR"
				}
				if entry["msg"] != operation+" "+phase || entry[field] != value || entry["level"] != level {
					t.Fatalf("unexpected operation log: %#v", entry)
				}
				if operation == "search" && phase == "completed" && entry["result_count"] != float64(1) {
					t.Fatalf("missing result count: %#v", entry)
				}
			}
		}
	}
}

func TestRequestLogs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		level  string
	}{
		{"success", 200, "INFO"},
		{"invalid request", 400, "WARN"},
		{"upstream failure", 502, "ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, nil))
			mux := http.NewServeMux()
			mux.HandleFunc("POST /test", func(w http.ResponseWriter, r *http.Request) {
				if tc.status != http.StatusOK {
					w.WriteHeader(tc.status)
				}
				_, _ = w.Write([]byte("response"))
			})
			req := httptest.NewRequest("POST", "/test?q=private-query", strings.NewReader("private-body"))
			req.Header.Set("Authorization", "Bearer private-token")
			response := httptest.NewRecorder()
			logRequests(mux, logger).ServeHTTP(response, req)
			if response.Code != tc.status || response.Body.String() != "response" {
				t.Fatalf("response changed: %d %q", response.Code, response.Body.String())
			}
			var entry struct {
				Level    string   `json:"level"`
				Method   string   `json:"method"`
				Route    string   `json:"route"`
				Status   int      `json:"status"`
				Duration *float64 `json:"duration_ms"`
			}
			if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			if entry.Level != tc.level || entry.Status != tc.status || entry.Method != "POST" || entry.Route != "POST /test" || entry.Duration == nil || *entry.Duration < 0 {
				t.Fatalf("unexpected log: %s", output.String())
			}
			if strings.Contains(output.String(), "private-") {
				t.Fatalf("request details leaked: %s", output.String())
			}
		})
	}
}

func TestDAVRequestLogs(t *testing.T) {
	d, fake, _ := davFake(t)
	var output bytes.Buffer
	h := (server{dav: d, logger: slog.New(slog.NewJSONHandler(&output, nil))}).routes()
	request := func(method, path, depth string) map[string]any {
		t.Helper()
		output.Reset()
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer private-authorization")
		if depth != "" {
			r.Header.Set("Depth", depth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var entry map[string]any
		decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
		for {
			var line map[string]any
			if err := decoder.Decode(&line); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("decode DAV request log: %v: %s", err, output.String())
			}
			if line["msg"] == "HTTP request" {
				entry = line
			}
		}
		if entry == nil {
			t.Fatalf("missing DAV request summary: %s", output.String())
		}
		for _, private := range []string{"private-authorization", "private-query", "private-media-token", "G8xag"} {
			if strings.Contains(output.String(), private) {
				t.Fatalf("DAV request log leaked %q: %s", private, output.String())
			}
		}
		if entry["route"] != "/dav/{path...}" || entry["status"] != float64(w.Code) || entry["bytes"] == nil || entry["duration_ms"] == nil {
			t.Fatalf("incomplete DAV request log: %s", output.String())
		}
		return entry
	}
	path := "/dav/catalog/latest/0001-SSIS-006/metadata.json?token=private-query"
	first := request("GET", path, "")
	if first["dav_source"] != "catalog" || first["dav_section"] != "latest" || first["dav_resource"] != "metadata" || first["dav_item"] != "0001-SSIS-006" || first["dav_file"] != "metadata.json" || first["dav_listing_cache"] != "miss" || first["dav_list_items"] != float64(1) {
		t.Fatalf("first DAV request log: %#v", first)
	}
	second := request("PROPFIND", "/dav/catalog/latest/", "1")
	if second["dav_depth"] != "1" || second["dav_listing_cache"] != "hit" {
		t.Fatalf("cached DAV request log: %#v", second)
	}
	searched := request("PROPFIND", "/dav/catalog/search/private-query/", "0")
	if searched["dav_section"] != "search" || searched["dav_item"] != nil {
		t.Fatalf("search path was not redacted: %#v", searched)
	}
	ordinalQuery := request("PROPFIND", "/dav/catalog/search/0001-private-query/", "0")
	if ordinalQuery["dav_item"] != nil {
		t.Fatalf("ordinal-shaped search query leaked: %#v", ordinalQuery)
	}
	media := request("GET", "/dav/catalog/latest/0001-SSIS-006/media/private-media-token", "")
	if media["dav_item"] != "0001-SSIS-006" {
		t.Fatalf("media log lacks safe item identity: %#v", media)
	}
	fake.failPage = 1
	d.flat.mu.Lock()
	d.flat.entries["movies:/catalog/latest"].expiresAt = time.Now().Add(-time.Second)
	d.flat.entries["movies:/catalog/latest"].fetchedAt = time.Now().Add(-flatMaxAge)
	d.flat.mu.Unlock()
	failed := request("PROPFIND", "/dav/catalog/latest/", "1")
	if failed["status"] != float64(502) || failed["level"] != "ERROR" || failed["dav_listing_cache"] != "refresh" || failed["dav_failure_stage"] != "movies" || failed["dav_failure_category"] != "upstream" {
		t.Fatalf("failed DAV request log: %#v", failed)
	}
}
