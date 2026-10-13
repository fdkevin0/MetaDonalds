package client

import (
	"context"
	"fmt"
	"github.com/fdkevin0/MetaDonalds/internal/core"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResourceSubmissionClassifiesRefusalAndAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		code         int
		body, status string
	}{{422, `{"detail":"bad resource"}`, "rejected"}, {502, `{"detail":"proxy failed"}`, "unknown"}, {200, `{"status":"accepted","info_hash":"wrong"}`, "unknown"}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.code); fmt.Fprint(w, tc.body) }))
		c, _ := New(srv.URL)
		result, err := c.SubmitResource(context.Background(), "destination", core.Resource{ID: "r", Title: "r", Kind: "magnet", InfoHash: strings.Repeat("a", 40)})
		srv.Close()
		if err == nil || result.Status != tc.status {
			t.Fatalf("HTTP %d: %+v %v", tc.code, result, err)
		}
	}
}
