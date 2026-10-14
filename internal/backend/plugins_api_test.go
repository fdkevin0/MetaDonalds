package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/fdkevin0/MetaDonalds/internal/core"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testHash = "0123456789abcdef0123456789abcdef01234567"

type fakeDownloader struct{ calls int }

func (d *fakeDownloader) Submit(_ context.Context, r core.Resource) core.Submission {
	d.calls++
	return core.Submission{Status: "accepted", InfoHash: r.InfoHash}
}
func TestGenericAPIs(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("f") != "2" || r.URL.Query().Get("c") != "1_0" {
			t.Errorf("filters lost: %s", r.URL.RawQuery)
		}
		io.WriteString(w, `<rss><channel><item><title>Example</title><infoHash>`+testHash+`</infoHash><seeders>7</seeders><size>1 GiB</size></item></channel></rss>`)
	}))
	defer upstream.Close()
	cfg := Config{Plugins: PluginConfig{Sources: []SourceConfig{{ID: "mirror", Type: "nyaa", BaseURL: upstream.URL}}}}
	service := Handler(cfg, nil)
	t.Cleanup(service.Close)
	var handler http.Handler = service
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/v1/sources/mirror/search?q=example&f=2&c=1_0"} {
		w := request("GET", path, "")
		if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("Example")) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	w := request("GET", "/api/v1/plugins", "")
	if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte("downloader")) {
		t.Fatalf("plugins: %s", w.Body)
	}
	w = request("GET", "/api/v1/sources/mirror/items/"+testHash+"/resources", "")
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(testHash)) {
		t.Fatal(w.Body.String())
	}
	if w = request("GET", "/api/v1/sources/absent/search?q=x", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	for _, q := range []string{"", strings.Repeat("a", 201), "%20"} {
		if w = request("GET", "/api/v1/sources/mirror/search?q="+q, ""); w.Code != 422 {
			t.Fatalf("invalid query code %d", w.Code)
		}
	}
	registry := core.NewRegistry()
	d := &fakeDownloader{}
	registry.AddDownloader("test", "test", []string{"magnet"}, d)
	handler = (server{registry: registry}).routes()
	for _, body := range []string{`{"resource":{"kind":"http","info_hash":"` + testHash + `"}}`, `{"resource":{"kind":"magnet","info_hash":"bad"}}`, `{"resource":{"kind":"magnet","info_hash":"` + testHash + `","url":"http://private"}}`} {
		if w = request("POST", "/api/v1/downloaders/test/submissions", body); w.Code != 422 {
			t.Fatalf("invalid resource code %d body %s", w.Code, w.Body)
		}
	}
	if d.calls != 0 {
		t.Fatal("invalid request reached downloader")
	}
	w = request("POST", "/api/v1/downloaders/test/submissions", `{"resource":{"id":"test","title":"Test","kind":"magnet","info_hash":"`+strings.ToUpper(testHash)+`"}}`)
	var result core.Submission
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || result.Status != "accepted" || result.InfoHash != testHash || d.calls != 1 {
		t.Fatalf("submission: %d %s", w.Code, w.Body)
	}
}
