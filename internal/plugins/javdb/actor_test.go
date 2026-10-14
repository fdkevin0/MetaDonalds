package javdb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestActorProfileValidationAndCache(t *testing.T) {
	for _, invalid := range []string{`null`, `{}`, `{"id":"wrong"}`, `{"id":"actor1","avatar_url":42}`} {
		t.Run(invalid, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/v1/actors/actor1" {
					t.Errorf("unexpected actor path: %s", r.URL.Path)
				}
				body := invalid
				if requests > 1 {
					body = `{"id":"actor1","name":"Actor","birthday":null,"height":"165","videos_count":95}`
				}
				fmt.Fprintf(w, `{"success":1,"data":{"actor":%s}}`, body)
			}))
			defer srv.Close()
			s := &Source{BaseURL: srv.URL, Client: srv.Client()}
			ctx := context.Background()
			if _, err := s.Actor(ctx, "../secret"); err == nil || requests != 0 {
				t.Fatal("invalid actor ID reached upstream")
			}
			if _, err := s.Actor(ctx, "actor1"); err == nil {
				t.Fatal("invalid actor profile accepted")
			}
			for range 2 {
				profile, err := s.Actor(ctx, "actor1")
				if err != nil || !strings.Contains(string(profile), `"birthday":null`) || !strings.Contains(string(profile), `"height":"165"`) {
					t.Fatalf("actor profile: %s %v", profile, err)
				}
			}
			if requests != 2 {
				t.Fatalf("invalid response cached or valid response refetched: %d requests", requests)
			}
		})
	}
}
