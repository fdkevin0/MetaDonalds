package backend

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/plugins/javdb"
)

func traceEntries(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var entries []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var entry map[string]any
		if err := decoder.Decode(&entry); err == io.EOF {
			return entries
		} else if err != nil {
			t.Fatalf("decode trace log: %v", err)
		}
		entries = append(entries, entry)
	}
}

func stageExists(entries []map[string]any, stage, outcome string) bool {
	for _, entry := range entries {
		if entry["stage"] == stage && (outcome == "" || entry["outcome"] == outcome) {
			return true
		}
	}
	return false
}

func TestDAVTraceAcrossSourceAndMedia(t *testing.T) {
	fakeDAV, _, _ := davFake(t)
	mediaTransport := fakeDAV.client.Transport
	client := &http.Client{Transport: davRoundTrip(func(r *http.Request) (*http.Response, error) {
		var body string
		switch r.URL.Path {
		case "/api/v1/movies/latest":
			body = `{"success":1,"data":{"movies":[{"id":"G8xag","number":"SSIS-006","release_date":"2020-01-02"}]}}`
		case "/api/v4/movies/G8xag":
			body = `{"success":1,"data":{"movie":{"id":"G8xag","number":"SSIS-006","title":"Title","release_date":"2020-01-02","cover_url":"https://media.example/cover.jpg?private-signed-query","preview_video_url":"https://media.example/preview.m3u8","preview_images":[{"large_url":"https://media.example/sample.jpg"}]}}}`
		default:
			return mediaTransport.RoundTrip(r)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
	})}
	source := &javdb.Source{ID: "catalog", BaseURL: "https://catalog.example/api", Client: client}
	d := newDAV(map[string]davCatalog{"catalog": source}, client, "https://reference.example").(*davServer)
	var output bytes.Buffer
	h := (server{dav: d, logger: slog.New(slog.NewJSONHandler(&output, nil))}).routes()
	request := func(path string) (int, string, []map[string]any) {
		t.Helper()
		output.Reset()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer private-auth")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if strings.Contains(output.String(), "private-auth") || strings.Contains(output.String(), "private-signed-query") || strings.Contains(output.String(), "private-search") || strings.Contains(output.String(), "G8xag") {
			t.Fatalf("private trace detail leaked: %s", output.String())
		}
		traceID := w.Header().Get("X-Trace-ID")
		if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(traceID) {
			t.Fatalf("missing response trace ID: %q", traceID)
		}
		entries := traceEntries(t, output.Bytes())
		seen := map[string]map[string]bool{}
		spanEvents := map[string]map[string]map[string]any{}
		for _, entry := range entries {
			id, _ := entry["trace_id"].(string)
			span, _ := entry["span_id"].(string)
			if id == "" || span == "" {
				t.Fatalf("trace identity missing: %#v", entry)
			}
			if seen[id] == nil {
				seen[id] = map[string]bool{}
				spanEvents[id] = map[string]map[string]any{}
			}
			seen[id][span] = true
			spanEvents[id][span] = entry
		}
		for _, entry := range entries {
			id, _ := entry["trace_id"].(string)
			if parent, _ := entry["parent_span_id"].(string); parent != "" && !seen[id][parent] {
				t.Fatalf("missing parent span %s in trace %s: %#v", parent, id, entry)
			} else if parent != "" {
				childEnd, childErr := time.Parse(time.RFC3339Nano, entry["time"].(string))
				parentEnd, parentErr := time.Parse(time.RFC3339Nano, spanEvents[id][parent]["time"].(string))
				if childErr != nil || parentErr != nil {
					t.Fatalf("invalid span time: child=%v parent=%v", childErr, parentErr)
				}
				childStart := childEnd.Add(-time.Duration(entry["duration_ms"].(float64) * float64(time.Millisecond)))
				if childStart.After(parentEnd.Add(time.Millisecond)) {
					t.Fatalf("child stage began after parent ended: child=%#v parent=%#v", entry, spanEvents[id][parent])
				}
			}
			if linked, _ := entry["linked_trace_id"].(string); linked != "" && seen[linked] == nil {
				t.Fatalf("missing linked flight trace %s: %#v", linked, entry)
			}
		}
		foundSummary := false
		for _, entry := range entries {
			if entry["msg"] == "HTTP request" && entry["trace_id"] == traceID {
				foundSummary = true
			}
		}
		if !foundSummary {
			t.Fatalf("response ID %s has no request summary: %#v", traceID, entries)
		}
		return w.Code, traceID, entries
	}
	base := "/dav/catalog/latest/0001-SSIS-006/"
	if code, _, entries := request(base + "metadata.json?private-search=1"); code != 200 || !stageExists(entries, "dav.list.fetch", "ok") || !stageExists(entries, "javdb.api_cache.fetch", "ok") || !stageExists(entries, "javdb.http", "ok") || !stageExists(entries, "dav.detail", "ok") {
		t.Fatalf("metadata trace lacks catalog stages: status=%d stages=%#v", code, entries)
	}
	if code, _, entries := request(base + "cover.jpg"); code != 200 || !stageExists(entries, "media.http", "ok") || !stageExists(entries, "dav.image.probe", "ok") || !stageExists(entries, "dav.image.decode", "ok") {
		t.Fatalf("cover trace lacks media stages: status=%d stages=%#v", code, entries)
	} else {
		readBytes := false
		for _, entry := range entries {
			if entry["stage"] == "media.http" && entry["method"] == "GET" && entry["bytes"].(float64) > 0 {
				readBytes = true
			}
			if entry["msg"] == "HTTP request" && entry["dav_file"] != "cover.jpg" {
				t.Fatalf("cover file label: %#v", entry)
			}
		}
		if !readBytes {
			t.Fatal("cover HTTP span did not measure body bytes")
		}
	}
	if code, _, entries := request(base + "images/01.jpg"); code != 200 {
		t.Fatalf("sample image status: %d", code)
	} else {
		for _, entry := range entries {
			if entry["msg"] == "HTTP request" && entry["dav_file"] != "images/01.jpg" {
				t.Fatalf("sample file label: %#v", entry)
			}
		}
	}
	if code, _, entries := request(base + "preview.m3u8"); code != 200 || !stageExists(entries, "dav.hls.rewrite", "ok") {
		t.Fatalf("preview trace: status=%d stages=%#v", code, entries)
	}
	if code, _, entries := request(base + "stream/video-2.mp4"); code != 200 || !stageExists(entries, "dav.resolve", "ok") || !stageExists(entries, "dav.proxy.copy", "ok") {
		t.Fatalf("stream trace: status=%d stages=%#v", code, entries)
	} else {
		copied := false
		for _, entry := range entries {
			if entry["stage"] == "dav.proxy.copy" && entry["bytes"] == float64(16) {
				copied = true
			}
		}
		if !copied {
			t.Fatalf("proxy copy did not measure delivered bytes: %#v", entries)
		}
	}
	response := davRequest(t, h, "GET", base+"metadata.json", "", "")
	conditional := httptest.NewRequest("GET", base+"metadata.json", nil)
	conditional.Header.Set("If-None-Match", response.Header().Get("ETag"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, conditional)
	if w.Code != http.StatusNotModified || w.Header().Get("X-Trace-ID") == "" {
		t.Fatalf("304 lacks trace identity: %d %#v", w.Code, w.Header())
	}
}

func TestDAVTraceInvalidCatalogResponse(t *testing.T) {
	for name, body := range map[string]string{
		"invalid_json":     `not JSON`,
		"invalid_identity": `{"success":1,"data":{"movie":{"id":"other"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			client := &http.Client{Transport: davRoundTrip(func(r *http.Request) (*http.Response, error) {
				responseBody := body
				if name == "invalid_identity" && r.URL.Path == "/api/v1/movies/latest" {
					responseBody = `{"success":1,"data":{"movies":[{"id":"G8xag","number":"SSIS-006"}]}}`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(responseBody)), ContentLength: int64(len(responseBody)), Request: r}, nil
			})}
			source := &javdb.Source{ID: "catalog", BaseURL: "https://catalog.example/api", Client: client}
			d := newDAV(map[string]davCatalog{"catalog": source}, client, "https://reference.example")
			var output bytes.Buffer
			h := (server{dav: d, logger: slog.New(slog.NewJSONHandler(&output, nil))}).routes()
			target := "/dav/catalog/latest/"
			if name == "invalid_identity" {
				target += "0001-SSIS-006/metadata.json"
			}
			w := davRequest(t, h, "PROPFIND", target, "1", "")
			if w.Code != http.StatusBadGateway || w.Header().Get("X-Trace-ID") == "" {
				t.Fatalf("failure lacks trace identity: %d %#v", w.Code, w.Header())
			}
			entries := traceEntries(t, output.Bytes())
			if !stageExists(entries, "javdb.validate", "error") || !stageExists(entries, "javdb.api_cache.fetch", "error") {
				t.Fatalf("validation failure not traced: %#v", entries)
			}
			for _, entry := range entries {
				if (entry["stage"] == "javdb.validate" || entry["stage"] == "javdb.api_cache.fetch") && entry["outcome"] == "error" {
					if entry["error_class"] != "invalid_response" {
						t.Fatalf("wrong validation class: %#v", entry)
					}
				}
			}
		})
	}
}
