package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

type server struct {
	nyaa   nyaaConfig
	search func(context.Context, nyaaConfig, string) ([]torrent, error)
	submit func(context.Context, string) error
}

type searchInput struct {
	Q          string `query:"q" required:"true" doc:"Search text; trimmed before validating a 1–200 UTF-8 byte limit." example:"Frieren"`
	F          int    `query:"f" minimum:"0" doc:"Nyaa filter. Omit to use the server configuration." example:"0"`
	C          string `query:"c" pattern:"^[0-9]+_[0-9]+$" doc:"Nyaa category. Omit to use the server configuration; 0_0 selects all categories." example:"0_0"`
	fSet, cSet bool
}

func (in *searchInput) Resolve(ctx huma.Context) []error {
	u := ctx.URL()
	params := u.Query()
	in.fSet, in.cSet = params.Has("f"), params.Has("c")
	var errs []error
	// Huma treats empty optional query values as absent; explicit overrides must not be empty.
	for _, name := range []string{"f", "c"} {
		if params.Has(name) && params.Get(name) == "" {
			errs = append(errs, &huma.ErrorDetail{Location: "query." + name, Message: "must not be empty"})
		}
	}
	in.Q = strings.TrimSpace(in.Q)
	if len(in.Q) == 0 || len(in.Q) > 200 {
		errs = append(errs, &huma.ErrorDetail{Location: "query.q", Message: "must be 1–200 bytes after trimming"})
	}
	return errs
}

type searchOutput struct {
	Body struct {
		Results []torrent `json:"results"`
	}
}

type submitInput struct {
	Body struct {
		InfoHash string `json:"info_hash" pattern:"^[0-9a-fA-F]{40}$" doc:"Torrent infoHash; normalized to lowercase." example:"0123456789abcdef0123456789abcdef01234567"`
	}
}

type submitOutput struct {
	Body struct {
		Status   string `json:"status" enum:"submitted"`
		InfoHash string `json:"info_hash" pattern:"^[0-9a-f]{40}$"`
	}
}

type healthOutput struct {
	ContentType string `header:"Content-Type"`
	Body        []byte
}

func (s server) routes() http.Handler {
	mux := http.NewServeMux()
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
	huma.Register(api, huma.Operation{
		OperationID: "search", Method: http.MethodGet, Path: "/api/v1/search", Summary: "Search Nyaa torrents",
		Description: "Search Nyaa RSS and rank results by seeders, trusted status, and remake status. Optional f and c override server configuration for this request only.",
		Errors:      []int{http.StatusBadGateway},
	}, func(ctx context.Context, in *searchInput) (*searchOutput, error) {
		cfg := s.nyaa
		if in.fSet {
			cfg.F = in.F
		}
		if in.cSet {
			cfg.C = in.C
		}
		slog.InfoContext(ctx, "search started", "query", in.Q, "f", cfg.F, "c", cfg.C)
		results, err := s.search(ctx, cfg, in.Q)
		if err != nil {
			slog.ErrorContext(ctx, "search failed", "query", in.Q)
			return nil, huma.Error502BadGateway("search failed")
		}
		slog.InfoContext(ctx, "search completed", "query", in.Q, "result_count", len(results))
		out := &searchOutput{}
		out.Body.Results = results
		return out, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "submit", Method: http.MethodPost, Path: "/api/v1/submit", Summary: "Submit a torrent to PikPak",
		Description:  "Submit an infoHash through rclone. Success means the command was accepted, not that the download finished. A timeout may leave the outcome uncertain; check PikPak before retrying. Maximum request body: 1024 bytes.",
		MaxBodyBytes: 1024, Errors: []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusBadGateway},
	}, func(ctx context.Context, in *submitInput) (*submitOutput, error) {
		hash := strings.ToLower(in.Body.InfoHash)
		slog.InfoContext(ctx, "submission started", "info_hash", hash)
		if err := s.submit(ctx, hash); err != nil {
			slog.ErrorContext(ctx, "submission failed", "info_hash", hash)
			return nil, huma.Error502BadGateway("submission failed")
		}
		slog.InfoContext(ctx, "submission accepted", "info_hash", hash)
		out := &submitOutput{}
		out.Body.Status, out.Body.InfoHash = "submitted", hash
		return out, nil
	})
	return logRequests(mux, slog.Default())
}
