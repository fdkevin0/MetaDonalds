package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
	d.flat.mu.Unlock()
	failed := request("PROPFIND", "/dav/catalog/latest/", "1")
	if failed["status"] != float64(502) || failed["level"] != "ERROR" || failed["dav_listing_cache"] != "refresh" || failed["dav_failure_stage"] != "movies" || failed["dav_failure_category"] != "upstream" {
		t.Fatalf("failed DAV request log: %#v", failed)
	}
}
