package javdb

import (
	"context"
	"crypto/md5"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnonymousReadProtocol(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		parts := strings.Split(r.Header.Get("jdsignature"), ".")
		if len(parts) != 3 || parts[1] != signatureID || parts[2] != fmt.Sprintf("%x", md5.Sum([]byte(parts[0]+signatureSalt))) {
			t.Error("invalid signature")
		}
		switch r.URL.Path {
		case "/v2/search":
			if r.URL.Query().Get("q") != "example" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("type") != "movie" {
				t.Errorf("query: %s", r.URL.RawQuery)
			}
			fmt.Fprint(w, `{"success":1,"data":{"movies":[{"id":"abc","title":"Example","number":"EX-1","score":"3.84"}]}}`)
		case "/v4/movies/abc":
			fmt.Fprint(w, `{"success":1,"data":{"movie":{"id":"abc","title":"Example","summary":"Detail","score":"3.84"}}}`)
		case "/v1/movies/abc/magnets":
			fmt.Fprintf(w, `{"success":1,"data":{"magnets":[{"name":"Resource","hash":"%s","size":5120},{"hash":"bad"},{"hash":"%s"}]}}`, strings.ToUpper(hash), hash)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	s := Source{ID: "catalog", BaseURL: server.URL, Client: server.Client()}
	ctx := context.Background()
	page, e := s.Search(ctx, "example", "2")
	if e != nil || len(page.Items) != 1 || page.Items[0].SourceID != "catalog" || page.Items[0].Score != "3.84" {
		t.Fatalf("search: %+v %v", page, e)
	}
	item, e := s.Detail(ctx, "abc")
	if e != nil || item.Summary != "Detail" {
		t.Fatalf("detail: %+v %v", item, e)
	}
	rs, e := s.Resources(ctx, "abc")
	if e != nil || len(rs) != 1 || rs[0].InfoHash != hash || rs[0].Size != "5120 (upstream units)" {
		t.Fatalf("resources: %+v %v", rs, e)
	}
	if _, e = s.Detail(ctx, "../secret"); e == nil {
		t.Fatal("path traversal accepted")
	}
	if _, e = s.Search(ctx, "x", "https://other"); e == nil {
		t.Fatal("URL cursor accepted")
	}
	if requests != 3 {
		t.Fatalf("unexpected request count: %d", requests)
	}
}
func TestBadEnvelopeAndEmptyResults(t *testing.T) {
	for _, body := range []string{`{"success":0,"data":{"movies":[]}}`, `{"success":1,"data":{}}`, `{"success":1,"data":null}`, `invalid`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer srv.Close()
			s := Source{BaseURL: srv.URL, Client: srv.Client()}
			if _, e := s.Search(context.Background(), "x", ""); e == nil {
				t.Fatal("bad response accepted")
			}
		})
	}
}
