package backend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestDAVMediaListingBoundsConcurrentProbes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var active, peak, calls atomic.Int32
		client := &http.Client{Transport: davRoundTrip(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			n := active.Add(1)
			defer active.Add(-1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(time.Second)
			if err := r.Context().Err(); err != nil {
				return nil, err
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: 10, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		})}
		d := newDAV(map[string]davCatalog{"catalog": &davCatalogFake{}}, client, "").(*davServer)
		defer d.flat.close()
		parent := "/catalog/latest/0001-MOVIE/media"
		for i := 0; i < 40; i++ {
			if _, err := d.tokens.add(fmt.Sprintf("https://media.example/%d.ts", i), parent, false); err != nil {
				t.Fatal(err)
			}
		}
		started := time.Now()
		nodes, err := d.children(context.Background(), parent, davNode{name: "media", parent: parent, dir: true})
		if err != nil || len(nodes) != 40 || calls.Load() != 40 || peak.Load() > 8 || time.Since(started) >= 30*time.Second {
			t.Fatalf("nodes=%d calls=%d peak=%d elapsed=%s error=%v", len(nodes), calls.Load(), peak.Load(), time.Since(started), err)
		}
	})
}

func TestDAVMediaListingFailureCancelsProbes(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: davRoundTrip(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	d := newDAV(map[string]davCatalog{"catalog": &davCatalogFake{}}, client, "").(*davServer)
	defer d.flat.close()
	parent := "/catalog/latest/0001-MOVIE/media"
	for i := 0; i < 40; i++ {
		if _, err := d.tokens.add(fmt.Sprintf("https://media.example/%d.ts", i), parent, false); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	nodes, err := d.children(ctx, parent, davNode{name: "media", parent: parent, dir: true})
	if !errors.Is(err, errMediaExpired) || len(nodes) != 0 || ctx.Err() != nil {
		t.Fatalf("partial or uncanceled listing: nodes=%d error=%v context=%v", len(nodes), err, ctx.Err())
	}
}

func TestDAVImageListingKeepsChecksumsWithoutBodies(t *testing.T) {
	for _, ranges := range []bool{false, true} {
		t.Run(fmt.Sprint(ranges), func(t *testing.T) {
			d, f, _ := davFake(t)
			data := append([]byte{0xff, 0xd8, 0xff, 0xe0}, make([]byte, (2<<20)-4)...)
			f.media.PreviewImages = nil
			for i := 0; i < 3; i++ {
				f.media.PreviewImages = append(f.media.PreviewImages, struct {
					LargeURL string `json:"large_url"`
				}{fmt.Sprintf("https://media.example/%d.jpg", i)})
			}
			var memo *davMemo
			full := 0
			d.client.Transport = davRoundTrip(func(r *http.Request) (*http.Response, error) {
				memo, _ = r.Context().Value(davMemoKey{}).(*davMemo)
				out, status := data, 200
				h := make(http.Header)
				if ranges && r.Header.Get("Range") != "" {
					out = data[:32]
					status = 206
					h.Set("Content-Range", fmt.Sprintf("bytes 0-31/%d", len(data)))
				} else {
					full++
				}
				h.Set("Content-Type", "image/jpeg")
				return &http.Response{StatusCode: status, Header: h, ContentLength: int64(len(out)), Body: io.NopCloser(bytes.NewReader(out)), Request: r}, nil
			})
			w := davRequest(t, d, "PROPFIND", "/dav/catalog/latest/0001-SSIS-006/images/", "1", "")
			if w.Code != 207 || full != 3 || len(memo.images) != 0 || len(memo.imageETags) != 3 || strings.Count(w.Body.String(), "<D:getetag>") != 3 {
				t.Fatalf("HTTP %d full=%d bodies=%d checksums=%d: %s", w.Code, full, len(memo.images), len(memo.imageETags), w.Body.String())
			}
		})
	}
}
