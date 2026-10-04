package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
		{`{"info_hash":"xyz"}`, http.StatusBadRequest},
		{`{"info_hash":"` + testHash + `","query":"Frieren"}`, http.StatusBadRequest},
		{`{"info_hash":"` + testHash + `"}{}`, http.StatusBadRequest},
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
		}
	}
	if called != 1 {
		t.Fatalf("submit called %d times", called)
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
		{"&f=", 0, "", 400},
		{"&f=nope", 0, "", 400},
		{"&f=-1", 0, "", 400},
		{"&c=", 0, "", 400},
		{"&c=bad", 0, "", 400},
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
			if tc.code == 400 {
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
