package desktop

import (
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/client"
	"github.com/fdkevin0/MetaDonalds/ui"
)

// handler serves local assets and forwards only the two application APIs.
// Keeping fetch requests same-origin lets browser and desktop share the frontend.
func handler(serverURL string) (http.Handler, error) {
	u, err := client.ParseURL(serverURL)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 60 * time.Second
	proxy := &httputil.ReverseProxy{
		Rewrite:   func(r *httputil.ProxyRequest) { r.SetURL(u) },
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"title":"无法连接 MetaDonalds 服务","status":502}`))
		},
	}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/search", proxy)
	mux.Handle("POST /api/v1/submit", proxy)
	mux.Handle("GET /", ui.Handler())
	return mux, nil
}
