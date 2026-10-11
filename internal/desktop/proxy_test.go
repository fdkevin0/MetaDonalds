package desktop

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestDesktopServesBuiltAssetsAndForwardsAPI(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/api/v1/submit" {
			t.Errorf("unexpected proxy request: %s %s", r.Method, r.URL)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"info_hash":"test"}` {
			t.Errorf("body changed: %s", body)
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":"invalid hash"}`))
	}))
	defer upstream.Close()
	handler, err := handler(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest("GET", "/", nil))
	if res.Code != 200 || !strings.Contains(res.Body.String(), "MetaDonalds") {
		t.Fatalf("frontend missing; run just frontend: %s", res.Body.String())
	}
	asset := regexp.MustCompile(`src="(/assets/[^" ]+\.js)"`).FindStringSubmatch(res.Body.String())
	if len(asset) != 2 {
		t.Fatal("built JS asset missing from page")
	}
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest("GET", asset[1], nil))
	if res.Code != 200 {
		t.Fatal("built JS not served")
	}
	res = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/submit", strings.NewReader(`{"info_hash":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(res, req)
	if res.Code != 422 || res.Body.String() != `{"detail":"invalid hash"}` || calls != 1 {
		t.Fatalf("proxy changed response or retried: %d %s, calls=%d", res.Code, res.Body.String(), calls)
	}
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest("GET", "/unrelated", nil))
	if res.Code != 404 || calls != 1 {
		t.Fatal("unrelated route proxied")
	}
}
