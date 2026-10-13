package pikpak

import (
	"context"
	"errors"
	"github.com/fdkevin0/MetaDonalds/internal/core"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSubmissionOutcomesAndNoRedirectRetry(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
		failure    bool
		want       string
	}{
		{"accepted", `{"result":{}}`, 200, false, "accepted"}, {"bad-json", `{}`, 200, false, "unknown"}, {"upstream-error", `{"error":"private"}`, 500, false, "unknown"}, {"lost-response", "", 0, true, "unknown"}, {"redirect", "", 307, false, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := NewRCClient("/unused")
			c.Transport = transport(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls > 1 {
					t.Fatal("submission retried")
				}
				if tc.failure {
					return nil, errors.New("lost response")
				}
				return &http.Response{StatusCode: tc.code, Header: http.Header{"Location": []string{"http://rclone/other"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			d := Downloader{Remote: "pikpak:", Client: c}
			result := d.Submit(context.Background(), core.Resource{Kind: "magnet", InfoHash: strings.Repeat("A", 40)})
			if result.Status != tc.want || result.InfoHash != strings.Repeat("a", 40) || calls != 1 {
				t.Fatalf("result %+v calls %d", result, calls)
			}
			if strings.Contains(result.Message, "private") {
				t.Fatal("upstream error leaked")
			}
			result = d.Submit(context.Background(), core.Resource{Kind: "http", InfoHash: strings.Repeat("a", 40)})
			if result.Status != "rejected" || calls != 1 {
				t.Fatal("invalid resource reached upstream")
			}
		})
	}
}
