package backend

import (
	"encoding/json"
	"fmt"
	"github.com/fdkevin0/MetaDonalds/internal/core"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubmitValidatesResource(t *testing.T) {
	registry := coreRegistry(t)
	d := registry.Downloaders["test"].(*fakeDownloader)
	handler := (server{registry: registry}).routes()
	valid := `{"resource":{"id":"chosen","title":"Chosen","kind":"magnet","info_hash":"` + strings.ToUpper(testHash) + `"}}`
	for _, tc := range []struct {
		body string
		code int
	}{
		{valid, 200},
		{strings.Replace(valid, strings.ToUpper(testHash), "bad", 1), 422},
		{strings.Replace(valid, `"magnet"`, `"http"`, 1), 422},
		{strings.Replace(valid, `"id":"chosen"`, `"id":"chosen","url":"http://private"`, 1), 422},
		{valid + `{}`, 400},
		{strings.Replace(valid, "Chosen", strings.Repeat("a", 8192), 1), 413},
	} {
		r := httptest.NewRequest("POST", "/api/v1/downloaders/test/submissions", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("HTTP %d, want %d: %s", w.Code, tc.code, w.Body.String())
		}
		if tc.code == 200 && !strings.Contains(w.Body.String(), testHash) {
			t.Fatal("hash not normalized")
		}
	}
	if d.calls != 1 {
		t.Fatalf("invalid input reached downloader: %d calls", d.calls)
	}
}

func TestSubmissionCrossOriginProtection(t *testing.T) {
	registry := coreRegistry(t)
	d := registry.Downloaders["test"].(*fakeDownloader)
	handler := (server{registry: registry}).routes()
	body := `{"resource":{"id":"chosen","title":"Chosen","kind":"magnet","info_hash":"` + testHash + `"}}`
	for _, tc := range []struct {
		origin, site string
		code         int
	}{
		{"http://foreign.example", "cross-site", 403},
		{"http://foreign.example", "", 403},
		{"http://127.0.0.1:8080", "same-origin", 200},
		{"", "", 200},
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/v1/downloaders/test/submissions", strings.NewReader(body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("Origin=%q site=%q HTTP %d: %s", tc.origin, tc.site, w.Code, w.Body.String())
		}
	}
	if d.calls != 2 {
		t.Fatalf("cross-origin request reached downloader: %d calls", d.calls)
	}
}

func TestSourceSearchValidationAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		params string
		f, c   string
		code   int
	}{
		{"q=+Frieren+", "2", "1_2", 200},
		{"q=Frieren&f=0", "0", "1_2", 200},
		{"q=Frieren&c=0_0", "2", "0_0", 200},
		{"q=Frieren&f=1&c=1_0", "1", "1_0", 200},
		{"", "", "", 422}, {"q=+++", "", "", 422},
		{"q=" + strings.Repeat("a", 201), "", "", 422},
		{"q=" + strings.Repeat("猫", 67), "", "", 422},
		{"q=Frieren&f=", "", "", 422}, {"q=Frieren&f=nope", "", "", 422},
		{"q=Frieren&f=-1", "", "", 422}, {"q=Frieren&c=", "", "", 422}, {"q=Frieren&c=bad", "", "", 422},
	} {
		t.Run(tc.params, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Query().Get("q") != "Frieren" || r.URL.Query().Get("f") != tc.f || r.URL.Query().Get("c") != tc.c {
					t.Errorf("wrong query: %s", r.URL)
				}
				fmt.Fprint(w, "<rss><channel></channel></rss>")
			}))
			defer upstream.Close()
			service := Handler(Config{Plugins: PluginConfig{Sources: []SourceConfig{{ID: "mirror", Type: "nyaa", BaseURL: upstream.URL, F: 2, C: "1_2"}}}}, nil)
			defer service.Close()
			w := httptest.NewRecorder()
			service.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/sources/mirror/search?"+tc.params, nil))
			wantCalls := 0
			if tc.code == 200 {
				wantCalls = 1
			}
			if w.Code != tc.code || calls != wantCalls {
				t.Fatalf("HTTP %d calls=%d: %s", w.Code, calls, w.Body.String())
			}
		})
	}
}

func TestAPIDocumentation(t *testing.T) {
	handler := (server{registry: coreRegistry(t)}).routes()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.json", nil))
	var spec struct {
		OpenAPI string
		Paths   map[string]json.RawMessage
	}
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil || w.Code != 200 || spec.OpenAPI != "3.1.0" {
		t.Fatalf("invalid OpenAPI: %s", w.Body.String())
	}
	for _, path := range []string{"/healthz", "/api/v1/plugins", "/api/v1/sources/{source}/search", "/api/v1/sources/{source}/items/{id}", "/api/v1/sources/{source}/items/{id}/resources", "/api/v1/downloaders/{downloader}/submissions"} {
		if spec.Paths[path] == nil {
			t.Fatalf("missing operation %s", path)
		}
	}
	if len(spec.Paths) != 6 {
		t.Fatalf("unexpected operations: %v", spec.Paths)
	}
	for _, path := range []string{"/docs", "/openapi.yaml", "/healthz"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s: HTTP %d", path, w.Code)
		}
		if path == "/healthz" && w.Body.String() != "ok\n" {
			t.Fatal("health contract changed")
		}
	}
}

func coreRegistry(t *testing.T) *core.Registry {
	t.Helper()
	r := core.NewRegistry()
	r.AddDownloader("test", "test", []string{"magnet"}, &fakeDownloader{})
	return r
}
