package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
				search: func(context.Context, string) ([]torrent, error) {
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
