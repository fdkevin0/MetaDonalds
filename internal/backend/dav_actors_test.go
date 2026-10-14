package backend

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fdkevin0/MetaDonalds/internal/plugins/javdb"
)

func TestDAVMovieActors(t *testing.T) {
	fake, _, _ := davFake(t)
	profileCalls := 0
	client := &http.Client{Transport: davRoundTrip(func(r *http.Request) (*http.Response, error) {
		var body string
		status := http.StatusOK
		switch r.URL.Path {
		case "/api/v1/movies/latest", "/api/v1/movies/tags":
			body = `{"success":1,"data":{"movies":[{"id":"G8xag","number":"SSIS-006","release_date":"2020-01-02"}]}}`
		case "/api/v4/movies/G8xag":
			body = `{"success":1,"data":{"movie":{"id":"G8xag","number":"SSIS-006","preview_video_url":"https://media.example/preview.m3u8","actors":[{"id":"actor1","name":"演员 A/B","gender":0,"avatar_url":"https://media.example/sample.jpg"},{"id":"actor2","name":"演员乙","gender":1,"avatar_url":null},{"id":"missing","name":"Unavailable"},{"id":"../secret","name":"Unsafe"},{"id":"actor1","name":"Duplicate"}]}}}`
		case "/api/v1/actors/actor1", "/api/v1/actors/actor2":
			profileCalls++
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/actors/")
			body = `{"success":1,"data":{"actor":{"id":"` + id + `","name":"演员","birthday":null,"height":165,"videos_count":95}}}`
		case "/api/v1/actors/missing":
			status = http.StatusBadGateway
		default:
			return fake.client.Transport.RoundTrip(r)
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
	})}
	source := &javdb.Source{ID: "catalog", BaseURL: "https://catalog.example/api", Client: client}
	d := newDAV(map[string]davCatalog{"catalog": source}, fake.client, "https://reference.example")
	root := "/dav/catalog/latest/0001-SSIS-006/"
	listing := davRequest(t, d, "PROPFIND", root, "1", "")
	if listing.Code != 207 || !strings.Contains(listing.Body.String(), root+"actors/") || !strings.Contains(listing.Body.String(), "preview.m3u8") || profileCalls != 0 {
		t.Fatalf("movie listing: %d profiles=%d %s", listing.Code, profileCalls, listing.Body.String())
	}
	actors := davRequest(t, d, "PROPFIND", root+"actors/", "1", "")
	if actors.Code != 207 || strings.Count(actors.Body.String(), "<D:response>") != 4 || strings.Contains(actors.Body.String(), "Unsafe") || strings.Contains(actors.Body.String(), "Duplicate") || profileCalls != 0 {
		t.Fatalf("actor listing: %d profiles=%d %s", actors.Code, profileCalls, actors.Body.String())
	}
	actor := root + "actors/" + url.PathEscape(davReadableName("演员 A/B", "actor1")) + "/"
	files := davRequest(t, d, "PROPFIND", actor, "1", "")
	if files.Code != 207 || !strings.Contains(files.Body.String(), "metadata.json") || !strings.Contains(files.Body.String(), "avatar.jpg") || !strings.Contains(files.Body.String(), "Thu, 02 Jan 2020") {
		t.Fatalf("actor files: %d %s", files.Code, files.Body.String())
	}
	for _, base := range []string{actor, "/dav/catalog/categories/censored/0001-SSIS-006/actors/" + url.PathEscape(davReadableName("演员 A/B", "actor1")) + "/"} {
		metadata := davRequest(t, d, "GET", base+"metadata.json", "", "")
		if metadata.Code != 200 || !strings.Contains(metadata.Body.String(), `"birthday": null`) || !strings.Contains(metadata.Body.String(), `"height": 165`) || !strings.Contains(metadata.Body.String(), `"videos_count": 95`) || metadata.Header().Get("ETag") == "" {
			t.Fatalf("actor metadata: %d %s", metadata.Code, metadata.Body.String())
		}
		photo := davRequest(t, d, "GET", base+"avatar.jpg", "", "bytes=0-2")
		if photo.Code != 206 || photo.Header().Get("Content-Range") != "bytes 0-2/18" || photo.Body.String() != "\xff\xd8\xff" || photo.Header().Get("Location") != "" {
			t.Fatalf("decoded actor photo: %d %x", photo.Code, photo.Body.Bytes())
		}
	}
	if profileCalls != 1 {
		t.Fatalf("actor profile cache missed: %d calls", profileCalls)
	}
	noPhoto := root + "actors/" + url.PathEscape(davReadableName("演员乙", "actor2")) + "/"
	files = davRequest(t, d, "PROPFIND", noPhoto, "1", "")
	if files.Code != 207 || strings.Contains(files.Body.String(), "avatar.jpg") {
		t.Fatalf("missing photo listing: %d %s", files.Code, files.Body.String())
	}
	for _, target := range []string{noPhoto + "avatar.jpg", actor + "metadata.json/extra", root + "actors/" + url.PathEscape(davReadableName("Other", "actor1")) + "/metadata.json"} {
		if w := davRequest(t, d, "GET", target, "", ""); w.Code != 404 {
			t.Fatalf("invalid actor path %s: %d", target, w.Code)
		}
	}
	unavailable := root + "actors/" + url.PathEscape(davReadableName("Unavailable", "missing")) + "/metadata.json"
	if w := davRequest(t, d, "GET", unavailable, "", ""); w.Code != 502 {
		t.Fatalf("unavailable actor: %d", w.Code)
	}
	if w := davRequest(t, d, "GET", root+"preview.m3u8", "", ""); w.Code != 200 || strings.Contains(w.Body.String(), "media.example") {
		t.Fatalf("preview after actor failure: %d %s", w.Code, w.Body.String())
	}
}

func TestDAVActorTraceLabels(t *testing.T) {
	if got := davFileLabel([]string{"actors", "Private Name [private-id]", "avatar.jpg"}); got != "actors/{actor}/avatar.jpg" {
		t.Fatalf("actor trace label exposes identity: %q", got)
	}
}
