package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClientContractAndNoSubmissionRetry(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	var submits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Path != "/api/v1/search" || r.URL.Query().Get("q") != "猫 & dogs" || !r.URL.Query().Has("f") || r.URL.Query().Get("f") != "0" || r.URL.Query().Has("c") {
				t.Errorf("incorrect search request: %s", r.URL)
			}
			_, _ = w.Write([]byte(`{"results":[{"title":"猫","info_hash":"` + hash + `"}]}`))
			return
		}
		submits.Add(1)
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/api/v1/submit" || body["info_hash"] != hash || r.Header.Get("Content-Type") != "application/json" {
			t.Error("incorrect submit request")
		}
		w.Header().Set("Location", "/api/v1/submit")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	c, err := New(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	items, err := c.Search(context.Background(), " 猫 & dogs ", "0", "")
	if err != nil || len(items) != 1 || items[0].InfoHash != hash {
		t.Fatalf("search: %v, %v", items, err)
	}
	err = c.Submit(context.Background(), hash)
	if err == nil || !strings.Contains(err.Error(), "check PikPak") || submits.Load() != 1 {
		t.Fatalf("unexpected retry or success: calls=%d error=%v", submits.Load(), err)
	}
}

func TestClientRejectsInvalidOriginAndSubmissionResponse(t *testing.T) {
	for _, raw := range []string{"file:///tmp/socket", "http://", "https://user:secret@example.com", "https://example.com/path", "https://example.com?q=x"} {
		if _, err := New(raw); err == nil {
			t.Errorf("accepted invalid origin %q", raw)
		}
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":"submitted"}`)) }))
	defer s.Close()
	c, _ := New(s.URL)
	if err := c.Submit(context.Background(), "0123456789abcdef0123456789abcdef01234567"); err == nil {
		t.Fatal("accepted missing echoed hash")
	}
}
