package backend

import (
	"context"
	"github.com/fdkevin0/MetaDonalds/internal/core"
	"log/slog"
	"net/http"
	"strings"

	"github.com/fdkevin0/MetaDonalds/ui"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

type server struct {
	registry *core.Registry
	logger   *slog.Logger
	dav      http.Handler
}

type healthOutput struct {
	ContentType string `header:"Content-Type"`
	Body        []byte
}

func (s server) routes() http.Handler {
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", ui.Handler())
	config := huma.DefaultConfig("MetaDonalds", "1.0.0")
	config.DocsRenderer = huma.DocsRendererScalar
	api := humago.New(mux, config)
	huma.Register(api, huma.Operation{
		OperationID: "health", Method: http.MethodGet, Path: "/healthz", Summary: "Check service health",
		Responses: map[string]*huma.Response{"200": {
			Description: "Service is running",
			Content:     map[string]*huma.MediaType{"text/plain": {Schema: &huma.Schema{Type: "string"}}},
		}},
	}, func(context.Context, *struct{}) (*healthOutput, error) {
		return &healthOutput{ContentType: "text/plain; charset=utf-8", Body: []byte("ok\n")}, nil
	})
	if s.registry != nil {
		s.pluginRoutes(api)
	}
	return logRequests(http.NewCrossOriginProtection().Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.dav != nil {
			if r.URL.Path == "/dav" {
				r.Pattern = "/dav"
				http.Redirect(w, r, "/dav/", http.StatusMovedPermanently)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/dav/") {
				r.Pattern = "/dav/{path...}"
				s.dav.ServeHTTP(w, r)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})), logger)
}
