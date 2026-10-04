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
		{`{"query":"Frieren"}`, http.StatusBadRequest},
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
