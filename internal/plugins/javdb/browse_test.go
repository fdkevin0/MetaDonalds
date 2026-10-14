package javdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestBrowseProtocol(t *testing.T) {
	requests := make(chan *url.URL, 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL
		if strings.HasPrefix(r.URL.Path, "/top250/") {
			movies := make([]map[string]string, 101)
			for i := range movies {
				movies[i] = map[string]string{"id": fmt.Sprintf("id%d", i), "number": fmt.Sprintf("CODE-%d", i)}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"movies": movies})
			return
		}
		body := `{"movies":[{"id":"abc","number":"EX-1"}]}`
		switch r.URL.Path {
		case "/v2/tags":
			body = `{"tags":[{"category_id":"main","category":"类别","tags":[{"id":123,"name":"剧情"}]}]}`
		case "/v1/actors":
			body = `{"actors":[{"id":"actor1","name":"演员甲","type":0}]}`
		case "/v1/actors/recommend":
			body = `{"recommend_actors":[{"id":"actor1","name":"演员甲","type":0}],"monthly_actors":[],"new_actors":[]}`
		case "/v1/series":
			body = `{"series":[{"id":"series1","name":"系列甲","type":2}]}`
		case "/v1/makers":
			body = `{"makers":[{"id":"maker1","name":"片商甲","type":3}]}`
		case "/v1/actors/actor1":
			body = `{"actor":{"id":"actor1","name":"演员甲","type":0}}`
		case "/v1/series/series1":
			body = `{"series":{"id":"series1","name":"系列甲","type":2}}`
		case "/v1/makers/maker1":
			body = `{"maker":{"id":"maker1","name":"片商甲","type":3}}`
		case "/v1/movies/tags", "/v1/rankings", "/v1/rankings/playback":
		default:
			t.Errorf("unexpected browse endpoint %s", r.URL)
		}
		fmt.Fprintf(w, `{"success":1,"data":%s}`, body)
	}))
	defer srv.Close()
	s := Source{ID: "catalog", BaseURL: srv.URL, Top250BaseURL: srv.URL + "/top250", Client: srv.Client()}
	ctx := context.Background()
	check := func(path string, query url.Values) {
		t.Helper()
		got := <-requests
		if got.Path != path || got.Query().Encode() != query.Encode() {
			t.Fatalf("request: %s, want %s?%s", got, path, query.Encode())
		}
	}
	for _, tt := range []struct {
		group, tag, filter string
	}{
		{"", "", "0:t:::::"},
		{"main", "123", "0:t:123::::"},
		{"year", "2025", "0:t:::2025::"},
		{"genre", "123", "0:t::123:::"},
	} {
		page, err := s.CategoryMovies(ctx, "censored", tt.group, tt.tag, 2)
		if err != nil || len(page.Items) != 1 || page.Items[0].Code != "EX-1" {
			t.Fatalf("category movies: %+v %v", page, err)
		}
		check("/v1/movies/tags", url.Values{"filter_by": {tt.filter}, "sort_by": {"release"}, "order_by": {"desc"}, "page": {"2"}, "limit": {"24"}})
	}
	for _, pageNum := range []int{11, 99} {
		if _, err := s.CategoryMovies(ctx, "censored", "", "", pageNum); err != nil {
			t.Fatalf("category page %d: %v", pageNum, err)
		}
		check("/v1/movies/tags", url.Values{"filter_by": {"0:t:::::"}, "sort_by": {"release"}, "order_by": {"desc"}, "page": {fmt.Sprint(pageNum)}, "limit": {"24"}})
	}
	tags, err := s.CategoryTags(ctx, "censored")
	if err != nil || len(tags) != 1 || tags[0].ID != "main" || len(tags[0].Tags) != 1 || tags[0].Tags[0].ID != "123" {
		t.Fatalf("category tags: %+v %v", tags, err)
	}
	check("/v2/tags", url.Values{"type": {"0"}})
	for _, tt := range []struct {
		kind, typ, period, path string
		query                   url.Values
	}{
		{"score", "western", "weekly", "/v1/rankings", url.Values{"type": {"2"}, "period": {"weekly"}}},
		{"playback", "all", "daily", "/v1/rankings/playback", url.Values{"filter_by": {"all"}, "period": {"daily"}}},
		{"playback", "high_score", "weekly", "/v1/rankings/playback", url.Values{"filter_by": {"high_score"}, "period": {"weekly"}}},
	} {
		page, err := s.RankingMovies(ctx, tt.kind, tt.typ, tt.period, 1)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("ranking: %+v %v", page, err)
		}
		check(tt.path, tt.query)
	}
	top, err := s.RankingMovies(ctx, "top250", "all", "", 2)
	if err != nil || len(top.Items) != 50 || top.Items[0].ID != "id50" {
		t.Fatalf("TOP250 page 2: %+v %v", top, err)
	}
	check("/top250/all.json", nil)
	for _, tt := range []struct {
		kind, typ, path, typeValue, id, name string
	}{
		{"actors", "censored", "/v1/actors", "0", "actor1", "演员甲"},
		{"series", "western", "/v1/series", "2", "series1", "系列甲"},
		{"makers", "fc2", "/v1/makers", "3", "maker1", "片商甲"},
	} {
		entities, err := s.Directory(ctx, tt.kind, tt.typ, 2)
		if err != nil || len(entities) != 1 || entities[0].ID != tt.id || entities[0].Name != tt.name {
			t.Fatalf("%s directory: %+v %v", tt.kind, entities, err)
		}
		check(tt.path, url.Values{"type": {tt.typeValue}, "page": {"2"}})
	}
	recommend, err := s.Directory(ctx, "actors", "recommend", 1)
	if err != nil || len(recommend) != 1 || recommend[0].ID != "actor1" {
		t.Fatalf("recommended actors: %+v %v", recommend, err)
	}
	check("/v1/actors/recommend", nil)
	for _, tt := range []struct {
		kind, id, letter, typeValue, detail string
	}{
		{"actors", "actor1", "a", "0", "/v1/actors/actor1"},
		{"series", "series1", "s", "2", "/v1/series/series1"},
		{"makers", "maker1", "m", "3", "/v1/makers/maker1"},
	} {
		page, err := s.EntityMovies(ctx, tt.kind, tt.id, 3)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("%s movies: %+v %v", tt.kind, page, err)
		}
		check(tt.detail, nil)
		check("/v1/movies/tags", url.Values{"filter_by": {tt.typeValue + ":" + tt.letter + ":" + tt.id + ":"}, "sort_by": {"release"}, "order_by": {"desc"}, "page": {"3"}, "limit": {"24"}})
	}
}

func TestBrowseRejectsMalformedOrUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":1,"data":{}}`)
	}))
	defer srv.Close()
	s := Source{BaseURL: srv.URL, Client: srv.Client()}
	ctx := context.Background()
	if _, err := s.CategoryMovies(ctx, "anime", "", "", 1); err == nil {
		t.Fatal("invented anime category accepted")
	}
	if _, err := s.RankingMovies(ctx, "score", "all", "daily", 1); err == nil {
		t.Fatal("unsupported all score ranking accepted")
	}
	if _, err := s.RankingMovies(ctx, "playback", "censored", "daily", 1); err == nil {
		t.Fatal("unsupported playback filter accepted")
	}
	if _, err := s.CategoryMovies(ctx, "censored", "", "", 100); err == nil {
		t.Fatal("category page 100 accepted")
	}
	if _, err := s.Directory(ctx, "actors", "western", 1); err == nil {
		t.Fatal("unsupported actor type accepted")
	}
	if _, err := s.CategoryTags(ctx, "censored"); err == nil {
		t.Fatal("missing category groups accepted")
	}
	if _, err := s.RankingMovies(ctx, "playback", "all", "daily", 1); err == nil {
		t.Fatal("missing ranked movies accepted")
	}
	if _, err := s.Directory(ctx, "actors", "censored", 1); err == nil {
		t.Fatal("missing actor list accepted")
	}
}
