package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func TestSubmitValidatesHash(t *testing.T) {
	called := 0
	s := server{submit: func(_ context.Context, hash string) error {
		called++
		if hash != testHash {
			t.Errorf("unexpected hash %q", hash)
		}
		return nil
	}}
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"info_hash":"` + strings.ToUpper(testHash) + `"}`, http.StatusOK},
		{`{"info_hash":"xyz"}`, http.StatusUnprocessableEntity},
		{`{"info_hash":"` + testHash + `","query":"Frieren"}`, http.StatusUnprocessableEntity},
		{`{"info_hash":"` + testHash + `"}{}`, http.StatusBadRequest},
		{`{"info_hash":"` + strings.Repeat("a", 1024) + `"}`, http.StatusRequestEntityTooLarge},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/submit", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		s.routes().ServeHTTP(res, req)
		if res.Code != tc.code {
			t.Errorf("body %q: got HTTP %d, want %d", tc.body, res.Code, tc.code)
		}
		if tc.code == http.StatusOK {
			var result map[string]string
			if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil || result["status"] != "submitted" || result["info_hash"] != testHash {
				t.Errorf("bad response: %s, %v", res.Body.String(), err)
			}
		} else {
			var problem huma.ErrorModel
			if err := json.Unmarshal(res.Body.Bytes(), &problem); err != nil || problem.Status != tc.code || !strings.HasPrefix(res.Header().Get("Content-Type"), "application/problem+json") {
				t.Errorf("expected Huma problem response: %s", res.Body.String())
			}
		}
	}
	if called != 1 {
		t.Fatalf("submit called %d times", called)
	}
}

func TestSearchQueryValidation(t *testing.T) {
	calls := 0
	s := server{search: func(_ context.Context, _ nyaaConfig, query string) ([]torrent, error) {
		calls++
		if query != "Frieren" {
			t.Errorf("query was not trimmed: %q", query)
		}
		return []torrent{{Title: "Frieren", InfoHash: testHash}}, nil
	}}
	handler := s.routes()
	for _, query := range []string{"", "q=", "q=+++", "q=" + strings.Repeat("a", 201), "q=" + strings.Repeat("猫", 67), "q=+Frieren+"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/search?"+query, nil))
		if query == "q=+Frieren+" {
			var body struct {
				Results []torrent `json:"results"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != 200 || len(body.Results) != 1 || body.Results[0].InfoHash != testHash {
				t.Fatalf("bad search response: %s", response.Body.String())
			}
		} else if response.Code != 422 {
			t.Errorf("query %q: got %d, want 422", query, response.Code)
		}
	}
	if calls != 1 {
		t.Fatalf("invalid queries reached upstream: %d calls", calls)
	}
}

func TestAPIDocumentation(t *testing.T) {
	// Read the published JSON, rather than Huma's write-only schema types.
	type schema struct {
		Ref     string `json:"$ref"`
		Default any
	}
	type media struct{ Schema schema }
	type operation struct {
		Parameters []struct {
			Name     string
			Required bool
			Schema   schema
		}
		RequestBody struct{ Content map[string]media }
		Responses   map[string]struct{ Content map[string]media }
	}
	handler := (server{}).routes()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/openapi.json", nil))
	var spec struct {
		OpenAPI    string
		Paths      map[string]struct{ Get, Post *operation }
		Components struct{ Schemas map[string]json.RawMessage }
	}
	if err := json.Unmarshal(response.Body.Bytes(), &spec); err != nil || response.Code != 200 || spec.OpenAPI != "3.1.0" {
		t.Fatalf("invalid OpenAPI response: %s", response.Body.String())
	}
	search := spec.Paths["/api/v1/search"].Get
	submit := spec.Paths["/api/v1/submit"].Post
	health := spec.Paths["/healthz"].Get
	if search == nil || submit == nil || health == nil || len(search.Parameters) != 3 {
		t.Fatal("missing API operations or search parameters")
	}
	for _, param := range search.Parameters {
		if param.Name == "q" && !param.Required {
			t.Fatal("q must be documented as required")
		}
		if (param.Name == "f" || param.Name == "c") && (param.Required || param.Schema.Default != nil) {
			t.Fatal("overrides must be optional without hardcoded defaults")
		}
	}
	ref := submit.RequestBody.Content["application/json"].Schema.Ref
	var body struct {
		Properties           map[string]struct{ Pattern string }
		AdditionalProperties bool
	}
	if err := json.Unmarshal(spec.Components.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")], &body); err != nil {
		t.Fatal(err)
	}
	if body.Properties["info_hash"].Pattern != infoHashPattern.String() || body.AdditionalProperties {
		t.Fatal("submit schema must validate info_hash and reject unknown fields")
	}
	if len(submit.Responses["502"].Content) == 0 || len(search.Responses["502"].Content) == 0 || len(health.Responses["200"].Content) != 1 {
		t.Fatal("missing upstream errors or health media type")
	}
	for _, path := range []string{"/docs", "/openapi.yaml", "/healthz"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 200 {
			t.Fatalf("%s: HTTP %d", path, response.Code)
		}
		if path == "/docs" && (!strings.Contains(response.Body.String(), "/openapi.json") || !strings.Contains(response.Body.String(), "@scalar/api-reference")) {
			t.Fatal("docs must load Scalar and the generated OpenAPI")
		}
		if path == "/healthz" && response.Body.String() != "ok\n" {
			t.Fatalf("health response changed: %q", response.Body.String())
		}
	}
}

func TestSearchOverrides(t *testing.T) {
	defaults := nyaaConfig{BaseURL: "https://mirror.example/", F: 2, C: "1_2"}
	for _, tc := range []struct {
		params string
		f      int
		c      string
		code   int
	}{
		{"", 2, "1_2", 200},
		{"&f=0", 0, "1_2", 200},
		{"&c=0_0", 2, "0_0", 200},
		{"&f=1&c=1_0", 1, "1_0", 200},
		{"&f=", 0, "", 422},
		{"&f=nope", 0, "", 422},
		{"&f=-1", 0, "", 422},
		{"&c=", 0, "", 422},
		{"&c=bad", 0, "", 422},
	} {
		t.Run(tc.params, func(t *testing.T) {
			calls := 0
			s := server{nyaa: defaults, search: func(_ context.Context, cfg nyaaConfig, query string) ([]torrent, error) {
				calls++
				want := defaults
				if calls == 1 {
					want.F, want.C = tc.f, tc.c
				}
				if cfg != want || query != "Frieren" {
					t.Errorf("search got %#v, %q; want %#v", cfg, query, want)
				}
				return []torrent{}, nil
			}}
			handler := s.routes()
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/search?q=Frieren"+tc.params, nil))
			if response.Code != tc.code {
				t.Fatalf("got status %d, want %d", response.Code, tc.code)
			}
			if tc.code >= 400 {
				if calls != 0 {
					t.Fatal("invalid parameters reached upstream")
				}
				return
			}
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/search?q=Frieren", nil))
			if calls != 2 {
				t.Fatalf("search called %d times", calls)
			}
		})
	}
}
