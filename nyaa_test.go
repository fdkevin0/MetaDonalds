package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

const testHash = "0123456789abcdef0123456789abcdef01234567"

func TestSearchNyaa(t *testing.T) {
	feed := fmt.Sprintf(`<rss xmlns:nyaa="http://nyaa.si/xmlns/nyaa"><channel>
	<item><title>trusted</title><nyaa:infoHash>%s</nyaa:infoHash><nyaa:seeders>10</nyaa:seeders><nyaa:size>1.5 GiB</nyaa:size><nyaa:trusted>Yes</nyaa:trusted><nyaa:remake>No</nyaa:remake></item>
	<item><title>remake</title><nyaa:infoHash>%s</nyaa:infoHash><nyaa:seeders>100</nyaa:seeders><nyaa:remake>Yes</nyaa:remake></item>
	</channel></rss>`, testHash, strings.Repeat("a", 40))
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "mirror.example" || r.URL.Path != "/nyaa/" || r.URL.Query().Get("page") != "rss" || r.URL.Query().Get("c") != "1_2" || r.URL.Query().Get("q") != "Frieren & friends" {
			t.Errorf("wrong RSS query: %s", r.URL.String())
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(feed))}, nil
	})}
	got, err := searchNyaa(context.Background(), client, "https://mirror.example/nyaa/", "Frieren & friends")
	if err != nil || len(got) != 2 {
		t.Fatalf("search: %#v, %v", got, err)
	}
	if got[0].InfoHash != testHash || got[0].Score <= got[1].Score || got[0].Size != "1.5 GiB" {
		t.Fatalf("unexpected results: %#v", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
