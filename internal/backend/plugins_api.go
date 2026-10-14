package backend

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fdkevin0/MetaDonalds/internal/core"
)

type sourceSearchInput struct {
	Source     string `path:"source"`
	Q          string `query:"q" required:"true"`
	Cursor     string `query:"cursor" maxLength:"20"`
	F          int    `query:"f" minimum:"0"`
	C          string `query:"c" pattern:"^[0-9]+_[0-9]+$"`
	fSet, cSet bool
}

func (in *sourceSearchInput) Resolve(ctx huma.Context) []error {
	u := ctx.URL()
	params := u.Query()
	in.fSet, in.cSet = params.Has("f"), params.Has("c")
	var errs []error
	// Huma treats empty optional query values as absent.
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

type sourceItemInput struct {
	Source string `path:"source"`
	ID     string `path:"id" minLength:"1" maxLength:"100" pattern:"^[A-Za-z0-9_-]+$"`
}

func (s server) pluginRoutes(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "plugins", Method: http.MethodGet, Path: "/api/v1/plugins", Summary: "List enabled plugin instances"}, func(context.Context, *struct{}) (*struct {
		Body struct {
			Plugins []core.Plugin `json:"plugins"`
		}
	}, error) {
		out := &struct {
			Body struct {
				Plugins []core.Plugin `json:"plugins"`
			}
		}{}
		out.Body.Plugins = s.registry.Plugins
		return out, nil
	})
	huma.Register(api, huma.Operation{OperationID: "source-search", Method: http.MethodGet, Path: "/api/v1/sources/{source}/search", Summary: "Search a source"}, func(ctx context.Context, in *sourceSearchInput) (*struct{ Body core.Page }, error) {
		source, ok := s.registry.Sources[in.Source]
		if !ok {
			return nil, huma.Error404NotFound("Source not found")
		}
		var page core.Page
		var err error
		if in.fSet || in.cSet {
			filtered, ok := source.(core.FilteredSearcher)
			if !ok {
				return nil, huma.Error422UnprocessableEntity("This source does not support filters")
			}
			var f *int
			var c *string
			if in.fSet {
				f = &in.F
			}
			if in.cSet {
				c = &in.C
			}
			page, err = filtered.SearchFiltered(ctx, in.Q, in.Cursor, f, c)
		} else {
			page, err = source.Search(ctx, in.Q, in.Cursor)
		}
		if err != nil {
			return nil, huma.Error502BadGateway("Source search failed")
		}
		return &struct{ Body core.Page }{page}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "source-detail", Method: http.MethodGet, Path: "/api/v1/sources/{source}/items/{id}", Summary: "Get item details"}, func(ctx context.Context, in *sourceItemInput) (*struct{ Body core.Item }, error) {
		source, ok := s.registry.Sources[in.Source]
		if !ok {
			return nil, huma.Error404NotFound("Source not found")
		}
		item, err := source.Detail(ctx, in.ID)
		if err != nil {
			return nil, huma.Error502BadGateway("Source detail failed")
		}
		return &struct{ Body core.Item }{item}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "source-resources", Method: http.MethodGet, Path: "/api/v1/sources/{source}/items/{id}/resources", Summary: "List item resources"}, func(ctx context.Context, in *sourceItemInput) (*struct {
		Body struct {
			Resources []core.Resource `json:"resources"`
		}
	}, error) {
		source, ok := s.registry.Sources[in.Source]
		if !ok {
			return nil, huma.Error404NotFound("Source not found")
		}
		resources, err := source.Resources(ctx, in.ID)
		if err != nil {
			return nil, huma.Error502BadGateway("Source resources failed")
		}
		out := &struct {
			Body struct {
				Resources []core.Resource `json:"resources"`
			}
		}{}
		out.Body.Resources = resources
		return out, nil
	})
	huma.Register(api, huma.Operation{OperationID: "downloader-submit", Method: http.MethodPost, Path: "/api/v1/downloaders/{downloader}/submissions", Summary: "Submit a resource", Description: "Acceptance is not download completion. Unknown outcomes must be checked before retrying.", MaxBodyBytes: 8192}, func(ctx context.Context, in *struct {
		Downloader string `path:"downloader"`
		Body       struct {
			Resource core.Resource `json:"resource"`
		}
	}) (*struct{ Body core.Submission }, error) {
		d, ok := s.registry.Downloaders[in.Downloader]
		if !ok {
			return nil, huma.Error404NotFound("Downloader not found")
		}
		r, err := core.NormalizeResource(in.Body.Resource)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		result := d.Submit(ctx, r)
		result.InfoHash = strings.ToLower(result.InfoHash)
		return &struct{ Body core.Submission }{result}, nil
	})
}
