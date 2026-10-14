package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fdkevin0/MetaDonalds/internal/core"
)

func TestClientContractAndNoSubmissionRetry(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	var submits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Path != "/api/v1/sources/nyaa/search" || r.URL.Query().Get("q") != "猫 & dogs" || r.URL.Query().Get("f") != "0" || r.URL.Query().Has("c") {
				t.Errorf("incorrect search request: %s", r.URL)
			}
			json.NewEncoder(w).Encode(core.Page{Items: []core.Item{{SourceID: "nyaa", Title: "猫", ID: hash}}})
			return
		}
		submits.Add(1)
		var body struct {
			Resource core.Resource `json:"resource"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/api/v1/downloaders/pikpak/submissions" || body.Resource.InfoHash != hash || r.Header.Get("Content-Type") != "application/json" {
			t.Error("incorrect submit request")
		}
		w.Header().Set("Location", r.URL.Path)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	page, err := c.SearchSource(context.Background(), "nyaa", "猫 & dogs", "", "0", "")
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != hash {
		t.Fatalf("page=%+v error=%v", page, err)
	}
	result, err := c.SubmitResource(context.Background(), "pikpak", core.Resource{Kind: "magnet", InfoHash: hash})
	if err == nil || result.Status != "unknown" || !strings.Contains(err.Error(), "check downloader") || submits.Load() != 1 {
		t.Fatalf("result=%+v error=%v calls=%d", result, err, submits.Load())
	}
}

func TestClientRejectsInvalidOrigin(t *testing.T) {
	for _, raw := range []string{"file:///tmp/socket", "http://", "https://user:secret@example.com", "https://example.com/path", "https://example.com?q=x"} {
		if _, err := New(raw); err == nil {
			t.Errorf("accepted invalid origin %q", raw)
		}
	}
}
