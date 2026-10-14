package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/core"
	"github.com/fdkevin0/MetaDonalds/internal/plugins/javdb"
)

type davCatalogFake struct {
	media       javdb.Media
	items       []core.Item
	pages       map[int]core.Page
	directories map[int][]javdb.BrowseEntity
	failPage    int
	others      map[string]javdb.Media
	fail        bool
}

func (f *davCatalogFake) Latest(_ context.Context, page int) (core.Page, error) {
	if f.failPage == page {
		return core.Page{}, fmt.Errorf("upstream page %d failed", page)
	}
	if f.pages != nil {
		return f.pages[page], nil
	}
	if page != 1 {
		return core.Page{Items: []core.Item{}}, nil
	}
	if f.items != nil {
		return core.Page{Items: f.items}, nil
	}
	return core.Page{Items: []core.Item{{ID: f.media.ID, Code: f.media.Number}}}, nil
}
func (f *davCatalogFake) Search(_ context.Context, q, cursor string) (core.Page, error) {
	if q != "SSIS" || cursor != "1" {
		return core.Page{Items: []core.Item{}}, nil
	}
	return f.Latest(context.Background(), 1)
}
func (f *davCatalogFake) Media(_ context.Context, id string) (javdb.Media, error) {
	if id == f.media.ID {
		return f.media, nil
	}
	if media, ok := f.others[id]; ok {
		return media, nil
	}
	return javdb.Media{}, os.ErrNotExist
}
func (f *davCatalogFake) Actor(_ context.Context, id string) (json.RawMessage, error) {
	for _, actor := range f.media.Actors {
		if actor.ID == id {
			return json.Marshal(actor)
		}
	}
	return nil, os.ErrNotExist
}
func (f *davCatalogFake) CategoryMovies(ctx context.Context, _, _, _ string, page int) (core.Page, error) {
	return f.Latest(ctx, page)
}
func (f *davCatalogFake) CategoryTags(context.Context, string) ([]javdb.BrowseTagGroup, error) {
	return []javdb.BrowseTagGroup{{ID: "main", Name: "类别", Tags: []javdb.BrowseTag{{ID: "123", Name: "剧情"}}}}, nil
}
func (f *davCatalogFake) RankingMovies(ctx context.Context, _, _, _ string, page int) (core.Page, error) {
	return f.Latest(ctx, page)
}
func (f *davCatalogFake) Directory(_ context.Context, _, _ string, page int) ([]javdb.BrowseEntity, error) {
	if f.failPage == page {
		return nil, fmt.Errorf("upstream page %d failed", page)
	}
	if f.directories != nil {
		return f.directories[page], nil
	}
	if page > 1 {
		return nil, nil
	}
	return []javdb.BrowseEntity{{ID: "actor1", Name: "演员 A/B"}}, nil
}
func (f *davCatalogFake) EntityMovies(ctx context.Context, _, _ string, page int) (core.Page, error) {
	return f.Latest(ctx, page)
}

type davRoundTrip func(*http.Request) (*http.Response, error)

func (f davRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func davFake(t *testing.T) (*davServer, *davCatalogFake, *int) {
	t.Helper()
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 0xff, 0xd9}
	encoded := []byte{0xde}
	for _, b := range jpeg {
		encoded = append(encoded, b^0xde)
	}
	media := javdb.Media{ID: "G8xag", Number: "SSIS-006", Title: "Title", Summary: "Summary", CoverURL: "https://media.example/cover.jpg", PreviewVideoURL: "https://media.example/preview.m3u8"}
	media.PreviewImages = append(media.PreviewImages, struct {
		LargeURL string `json:"large_url"`
	}{"https://media.example/sample.jpg"})
	f := &davCatalogFake{media: media}
	resolveCalls := 0
	client := &http.Client{Transport: davRoundTrip(func(r *http.Request) (*http.Response, error) {
		var body []byte
		status := http.StatusOK
		contentType := "application/octet-stream"
		switch r.URL.Path {
		case "/api/v/resolve":
			resolveCalls++
			if f.fail {
				status = http.StatusNotFound
				body = []byte(`{"error":"video_not_found"}`)
			} else {
				body = []byte(`{"variants":[{"sourceUrl":"https://media.example/master.m3u8","sourceType":"hls"},{"sourceUrl":"https://media.example/movie.mp4","sourceType":"video/mp4"}]}`)
			}
			contentType = "application/json"
		case "/cover.jpg", "/sample.jpg":
			body = encoded
			contentType = "image/jpeg"
		case "/preview.m3u8", "/master.m3u8":
			body = []byte("#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXT-X-STREAM-INF:BANDWIDTH=1000\nchild.m3u8\n")
			contentType = "application/vnd.apple.mpegurl"
		case "/child.m3u8":
			body = []byte("#EXTM3U\n#EXTINF:10,\nseg.ts\n")
			contentType = "application/vnd.apple.mpegurl"
		case "/key.bin":
			body = []byte("0123456789abcdef")
		case "/init.mp4":
			body = []byte("init")
		case "/seg.ts":
			body = []byte("segment data")
		case "/movie.mp4":
			body = []byte{0, 0, 0, 16, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0}
		default:
			t.Errorf("unexpected media request %s", r.URL)
			status = http.StatusNotFound
		}
		headers := http.Header{"Content-Type": []string{contentType}}
		total := len(body)
		if value := r.Header.Get("Range"); value != "" && status == 200 {
			var start, end int
			if _, err := fmt.Sscanf(value, "bytes=%d-%d", &start, &end); err == nil && start >= 0 && start < total {
				if end >= total {
					end = total - 1
				}
				body = body[start : end+1]
				status = http.StatusPartialContent
				headers.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
			} else {
				status = http.StatusRequestedRangeNotSatisfiable
				body = nil
				headers.Set("Content-Range", fmt.Sprintf("bytes */%d", total))
			}
		}
		headers.Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodHead {
			body = nil
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(totalIfRange(status, headers, total)), Request: r}, nil
	})}
	d := newDAV(map[string]davCatalog{"catalog": f}, client, "https://reference.example").(*davServer)
	t.Cleanup(d.flat.close)
	return d, f, &resolveCalls
}

func totalIfRange(status int, headers http.Header, total int) int {
	if status == http.StatusPartialContent {
		n, _ := strconv.Atoi(headers.Get("Content-Length"))
		return n
	}
	return total
}

func davRequest(t *testing.T, h http.Handler, method, target, depth, rng string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	if depth != "" {
		r.Header.Set("Depth", depth)
	}
	if rng != "" {
		r.Header.Set("Range", rng)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestDAVCatalogImagesAndHLS(t *testing.T) {
	d, f, resolves := davFake(t)
	f.media.ReleaseDate = "2020-01-02"
	f.items = []core.Item{{ID: f.media.ID, Code: f.media.Number, ReleaseDate: f.media.ReleaseDate}}
	root := "/dav/catalog/latest/0001-SSIS-006"
	displayDate := "Thu, 02 Jan 2020 00:00:00 GMT"
	for _, p := range []string{"/dav/", "/dav/catalog/latest/", "/dav/catalog/latest/", root + "/"} {
		w := davRequest(t, d, "PROPFIND", p, "1", "")
		if w.Code != 207 {
			t.Fatalf("PROPFIND %s: %d %s", p, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "collection") {
			t.Fatal("missing DAV collection property")
		}
	}
	if *resolves != 0 {
		t.Fatalf("item browsing resolved video %d times", *resolves)
	}
	if w := davRequest(t, d, "GET", root+"/metadata.json", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "SSIS-006") {
		t.Fatalf("metadata %d %s", w.Code, w.Body.String())
	}
	cover := davRequest(t, d, "GET", root+"/cover.jpg", "", "bytes=0-2")
	if cover.Code != 206 || !bytes.Equal(cover.Body.Bytes(), []byte{0xff, 0xd8, 0xff}) || cover.Header().Get("Content-Range") != "bytes 0-2/18" {
		t.Fatalf("decoded cover range: %d %s %x", cover.Code, cover.Header().Get("Content-Range"), cover.Body.Bytes())
	}
	if w := davRequest(t, d, "HEAD", root+"/cover.jpg", "", ""); w.Code != 200 || w.Header().Get("Content-Length") != "18" {
		t.Fatalf("image HEAD %d %s", w.Code, w.Header().Get("Content-Length"))
	}
	if w := davRequest(t, d, "PROPFIND", root+"/cover.jpg", "0", ""); w.Code != 207 || !strings.Contains(w.Body.String(), "<D:getcontentlength>18</D:getcontentlength>") {
		t.Fatalf("image DAV size: %d %s", w.Code, w.Body.String())
	}
	playlist := davRequest(t, d, "GET", root+"/preview.m3u8", "", "")
	if playlist.Code != 200 || strings.Contains(playlist.Body.String(), "media.example") {
		t.Fatalf("rewritten playlist: %d %s", playlist.Code, playlist.Body.String())
	}
	if head := davRequest(t, d, "HEAD", root+"/preview.m3u8", "", ""); head.Header().Get("Content-Length") != strconv.Itoa(playlist.Body.Len()) {
		t.Fatalf("manifest size HEAD=%s GET=%d", head.Header().Get("Content-Length"), playlist.Body.Len())
	}
	links := regexp.MustCompile(`media/([a-f0-9]{32}\.[a-z0-9]+)`).FindAllStringSubmatch(playlist.Body.String(), -1)
	if len(links) != 3 {
		t.Fatalf("expected key, map and subplaylist: %s", playlist.Body.String())
	}
	for _, link := range links {
		p := root + "/" + link[0]
		if w := davRequest(t, d, "PROPFIND", p, "0", ""); w.Code != 207 || !strings.Contains(w.Body.String(), displayDate) {
			t.Fatalf("token stat/date %s: %d %s", p, w.Code, w.Body.String())
		}
		if w := davRequest(t, d, "HEAD", p, "", ""); w.Code != 200 || w.Header().Get("Content-Length") == "0" {
			t.Fatalf("token HEAD %s: %d %s", p, w.Code, w.Header().Get("Content-Length"))
		}
		if w := davRequest(t, d, "GET", p, "", ""); w.Code != 200 {
			t.Fatalf("token GET %s: %d", p, w.Code)
		}
	}
	if w := davRequest(t, d, "PROPFIND", root+"/media/", "1", ""); w.Code != 207 || !strings.Contains(w.Body.String(), links[0][1]) {
		t.Fatalf("media dir %d %s", w.Code, w.Body.String())
	}
	child := davRequest(t, d, "GET", root+"/"+links[2][0], "", "")
	if child.Code != 200 || strings.Contains(child.Body.String(), "media/media/") || !regexp.MustCompile(`^[a-f0-9]{32}\.ts$`).MatchString(strings.TrimSpace(strings.Split(child.Body.String(), "\n")[2])) {
		t.Fatalf("child playlist: %d %s", child.Code, child.Body.String())
	}
	stream := davRequest(t, d, "PROPFIND", root+"/stream/", "1", "")
	if stream.Code != 207 || !strings.Contains(stream.Body.String(), "video-1.m3u8") || !strings.Contains(stream.Body.String(), "video-2.mp4") || !strings.Contains(stream.Body.String(), displayDate) {
		t.Fatalf("stream listing %d %s", stream.Code, stream.Body.String())
	}
	if w := davRequest(t, d, "PROPFIND", root+"/stream/video-2.mp4", "0", ""); w.Code != 207 || !strings.Contains(w.Body.String(), displayDate) {
		t.Fatalf("stream video date: %d %s", w.Code, w.Body.String())
	}
	video := davRequest(t, d, "GET", root+"/stream/video-2.mp4", "", "bytes=2-5")
	if video.Code != 206 || !bytes.Equal(video.Body.Bytes(), []byte{0, 16, 'f', 't'}) || video.Header().Get("Content-Range") != "bytes 2-5/16" {
		t.Fatalf("video range %d %s", video.Code, video.Body.String())
	}
	if w := davRequest(t, d, "HEAD", root+"/stream/video-2.mp4", "", ""); w.Code != 200 || w.Header().Get("Content-Length") != "16" {
		t.Fatalf("video HEAD: %d %s", w.Code, w.Header().Get("Content-Length"))
	}
	for _, method := range []string{"PUT", "DELETE", "MKCOL", "MOVE", "COPY", "LOCK", "PROPPATCH"} {
		if w := davRequest(t, d, method, root+"/metadata.json", "", ""); w.Code != 403 {
			t.Fatalf("%s allowed: %d", method, w.Code)
		}
	}
	if w := davRequest(t, d, "PROPFIND", "/dav/", "infinity", ""); w.Code != 403 {
		t.Fatalf("infinite listing allowed: %d", w.Code)
	}
}

func TestDAVResolveFailureOnlyAffectsStreamAndTokenExpiry(t *testing.T) {
	d, f, _ := davFake(t)
	f.fail = true
	root := "/dav/catalog/latest/0001-SSIS-006"
	if w := davRequest(t, d, "PROPFIND", root+"/", "1", ""); w.Code != 207 {
		t.Fatalf("item lost when resolver failed: %d", w.Code)
	}
	if w := davRequest(t, d, "PROPFIND", root+"/stream/", "1", ""); w.Code != 502 {
		t.Fatalf("resolver failure: %d", w.Code)
	}
	f.fail = false
	playlist := davRequest(t, d, "GET", root+"/preview.m3u8", "", "")
	link := regexp.MustCompile(`media/[a-f0-9]{32}\.bin`).FindString(playlist.Body.String())
	if link == "" {
		t.Fatal("no token")
	}
	key := strings.TrimPrefix(link, "media/")
	d.tokens.mu.Lock()
	entry := d.tokens.entries[key]
	entry.expires = time.Now().Add(-time.Second)
	d.tokens.entries[key] = entry
	d.tokens.mu.Unlock()
	if w := davRequest(t, d, "GET", root+"/"+link, "", ""); w.Code != 410 {
		t.Fatalf("expired token: %d", w.Code)
	}
	for _, raw := range []string{"http://127.0.0.1/secret", "http://169.254.169.254/latest/meta-data", "http://[::1]/x", "https://localhost/x"} {
		if validateMediaURL(raw) == nil {
			t.Fatalf("unsafe URL accepted: %s", raw)
		}
	}
}

func TestDAVDataPlaylistAndRanges(t *testing.T) {
	d, _, _ := davFake(t)
	parent := "/catalog/latest/0001-SSIS-006/media"
	raw := "data:application/vnd.apple.mpegurl,%23EXTM3U%0A%23EXT-X-STREAM-INF%3ABANDWIDTH%3D1%0A%2Fgg%2Fchild%3Ftoken%3Dsample%0A"
	data, err := d.playlistData(context.Background(), raw, "media/", parent)
	if err != nil || !regexp.MustCompile(`media/[a-f0-9]{32}`).Match(data) || strings.Contains(string(data), "/gg/") {
		t.Fatalf("data playlist: %s %v", data, err)
	}
	key := regexp.MustCompile(`[a-f0-9]{32}`).FindString(string(data))
	entry, err := d.tokens.get(key, parent)
	if err != nil || !entry.playlist || entry.url != "https://reference.example/gg/child?token=sample" {
		t.Fatalf("data playlist link: %+v %v", entry, err)
	}
	root := "/dav/catalog/latest/0001-SSIS-006"
	if w := davRequest(t, d, "GET", root+"/stream/video-2.mp4", "", "bytes=100-200"); w.Code != 416 || w.Header().Get("Content-Range") != "bytes */16" {
		t.Fatalf("unsatisfiable range: %d %s", w.Code, w.Header().Get("Content-Range"))
	}
	base, _ := url.Parse("https://media.example/")
	if _, err := d.rewritePlaylist("#EXTM3U\n#EXT-X-ENDLIST\n", base, "media/", parent); err == nil {
		t.Fatal("empty HLS source accepted")
	}
	d.client = &http.Client{Transport: davRoundTrip(func(r *http.Request) (*http.Response, error) {
		body := []byte("<!doctype html>bad")
		status := http.StatusOK
		if r.Method == http.MethodHead {
			body = nil
		} else {
			status = http.StatusPartialContent
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Length": []string{"18"}}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: 18, Request: r}, nil
	})}
	if _, err := d.variantNode(context.Background(), "video-1.mp4", mediaVariant{url: "https://media.example/movie.mp4", kind: "proxy"}, parent); err == nil {
		t.Fatal("HTML response accepted as MP4")
	}
}

func TestDAVPreviewParentAndPlaylistETag(t *testing.T) {
	d, _, _ := davFake(t)
	root := "/dav/catalog/latest/0001-SSIS-006/"
	listed := davRequest(t, d, "PROPFIND", root, "1", "")
	if listed.Code != 207 || !strings.Contains(listed.Body.String(), root+"media/") {
		t.Fatalf("preview media parent missing from item listing: %d %s", listed.Code, listed.Body.String())
	}
	target := root + "preview.m3u8"
	etag := regexp.MustCompile(`<D:getetag>([^<]+)</D:getetag>`)
	first := etag.FindStringSubmatch(davRequest(t, d, "PROPFIND", target, "0", "").Body.String())
	if len(first) != 2 {
		t.Fatal("playlist PROPFIND has no ETag")
	}
	for _, method := range []string{"GET", "HEAD"} {
		got := davRequest(t, d, method, target, "", "")
		if got.Code != 200 || got.Header().Get("ETag") != first[1] {
			t.Fatalf("%s playlist ETag differs from PROPFIND: %d %q vs %q", method, got.Code, got.Header().Get("ETag"), first[1])
		}
	}
	if same := etag.FindStringSubmatch(davRequest(t, d, "PROPFIND", target, "0", "").Body.String()); len(same) != 2 || same[1] != first[1] {
		t.Fatalf("unchanged playlist ETag changed: %v vs %v", same, first)
	}
	d.tokens = newMediaTokens()
	d.playlists = newPlaylistCache()
	second := etag.FindStringSubmatch(davRequest(t, d, "PROPFIND", target, "0", "").Body.String())
	if len(second) != 2 || second[1] == first[1] {
		t.Fatalf("different same-length playlist kept old ETag: %v vs %v", second, first)
	}
}

func TestDAVTokenCapacityPreservesActivePlaylists(t *testing.T) {
	d, _, _ := davFake(t)
	root := "/dav/catalog/latest/0001-SSIS-006/"
	first := davRequest(t, d, "GET", root+"preview.m3u8", "", "")
	if first.Code != 200 {
		t.Fatalf("initial playlist: %d", first.Code)
	}
	key := regexp.MustCompile(`media/[a-f0-9]{32}\.bin`).FindString(first.Body.String())
	if key == "" {
		t.Fatal("playlist lacks key token")
	}
	for i := len(d.tokens.entries); i < 10000; i++ {
		if _, err := d.tokens.add(fmt.Sprintf("https://cdn.example/%d.ts", i), "/elsewhere", false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.tokens.add("https://cdn.example/overflow.ts", "/elsewhere", false); err != errTokenCapacity {
		t.Fatalf("capacity should reject without eviction: %v", err)
	}
	reopened := davRequest(t, d, "GET", root+"preview.m3u8", "", "")
	if !bytes.Equal(first.Body.Bytes(), reopened.Body.Bytes()) {
		t.Fatal("cached playlist changed at capacity")
	}
	if got := davRequest(t, d, "GET", root+key, "", ""); got.Code != 200 {
		t.Fatalf("active playlist token was evicted: %d", got.Code)
	}

	d2, _, _ := davFake(t)
	for i := 0; i < 9999; i++ {
		if _, err := d2.tokens.add(fmt.Sprintf("https://cdn.example/%d.ts", i), "/elsewhere", false); err != nil {
			t.Fatal(err)
		}
	}
	base, _ := url.Parse("https://cdn.example/master.m3u8")
	_, err := d2.rewritePlaylist("#EXTM3U\n#EXTINF:1,\na.ts\n#EXTINF:1,\nb.ts\n", base, "media/", "/catalog/latest/0001-SSIS-006/media")
	if err != errTokenCapacity || len(d2.tokens.entries) != 9999 || len(d2.tokens.list("/catalog/latest/0001-SSIS-006/media")) != 0 {
		t.Fatalf("failed playlist left new tokens: err=%v count=%d", err, len(d2.tokens.entries))
	}
}

func TestDAVRedirectedPlaylistRelativeLinks(t *testing.T) {
	d, _, _ := davFake(t)
	d.client = &http.Client{Transport: davRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/start.m3u8" {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://cdn.example/nested/master.m3u8"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		body := "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n#EXT-X-STREAM-INF:BANDWIDTH=1\nchild.m3u8\n"
		if r.URL.Path == "/nested/child.m3u8" {
			body = "#EXTM3U\n#EXTINF:1,\nseg.ts\n"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	parent := "/catalog/latest/0001-SSIS-006/media"
	data, err := d.playlistData(context.Background(), "https://origin.example/start.m3u8", "media/", parent)
	if err != nil {
		t.Fatal(err)
	}
	key := regexp.MustCompile(`[a-f0-9]{32}\.bin`).FindString(string(data))
	child := regexp.MustCompile(`[a-f0-9]{32}\.m3u8`).FindString(string(data))
	for name, want := range map[string]string{key: "https://cdn.example/nested/key.bin", child: "https://cdn.example/nested/child.m3u8"} {
		entry, err := d.tokens.get(name, parent)
		if err != nil || entry.url != want {
			t.Fatalf("redirected link %s: %+v %v, want %s", name, entry, err, want)
		}
	}
	childEntry, _ := d.tokens.get(child, parent)
	childData, err := d.playlistData(context.Background(), childEntry.url, "", parent)
	if err != nil {
		t.Fatal(err)
	}
	segment := regexp.MustCompile(`[a-f0-9]{32}\.ts`).FindString(string(childData))
	entry, err := d.tokens.get(segment, parent)
	if err != nil || entry.url != "https://cdn.example/nested/seg.ts" {
		t.Fatalf("redirected child segment: %+v %v", entry, err)
	}
}

func TestDAVProxyFullResponseToRange(t *testing.T) {
	d, _, _ := davFake(t)
	d.client = &http.Client{Transport: davRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Range") != "bytes=4-7" {
			t.Fatal("range header not forwarded")
		}
		headers := http.Header{}
		headers.Set("ETag", `"new"`)
		headers.Set("Content-Length", "12")
		return &http.Response{StatusCode: 200, Header: headers, ContentLength: 12, Body: io.NopCloser(strings.NewReader("fullnewvideo")), Request: r}, nil
	})}
	for _, ifRange := range []string{`"old"`, ""} {
		r := httptest.NewRequest("GET", "/dav/anything.mp4", nil)
		r.Header.Set("Range", "bytes=4-7")
		r.Header.Set("If-Range", ifRange)
		w := httptest.NewRecorder()
		d.proxyMedia(w, r, "https://media.example/anything.mp4")
		if w.Code != 200 || w.Body.String() != "fullnewvideo" || w.Header().Get("ETag") != `"new"` {
			t.Fatalf("full response to range If-Range=%q: status=%d body=%q", ifRange, w.Code, w.Body.String())
		}
	}
}

func TestDAVNestedMediaDirectory(t *testing.T) {
	d, _, _ := davFake(t)
	root := "/dav/catalog/latest/0001-SSIS-006/"
	playlist := davRequest(t, d, "GET", root+"preview.m3u8", "", "")
	child := regexp.MustCompile(`media/[a-f0-9]{32}\.m3u8`).FindString(playlist.Body.String())
	if child == "" {
		t.Fatal("preview playlist has no nested playlist")
	}
	directory := davRequest(t, d, "PROPFIND", root+"media/", "1", "")
	if directory.Code != 207 {
		t.Fatalf("media directory: %d %s", directory.Code, directory.Body.String())
	}
	nested := davRequest(t, d, "GET", root+child, "", "")
	segment := regexp.MustCompile(`[a-f0-9]{32}\.ts`).FindString(nested.Body.String())
	if segment == "" || !strings.Contains(directory.Body.String(), segment) {
		t.Fatalf("media listing omitted nested segment %q: %s", segment, directory.Body.String())
	}
	parent := "/catalog/latest/0001-SSIS-006/media"
	for i := len(d.tokens.list(parent)); i <= 2000; i++ {
		if _, err := d.tokens.add(fmt.Sprintf("https://media.example/extra-%d.ts", i), parent, false); err != nil {
			t.Fatal(err)
		}
	}
	if over := davRequest(t, d, "PROPFIND", root+"media/", "1", ""); over.Code != 503 {
		t.Fatalf("oversized media directory was not rejected: %d", over.Code)
	}
}

func TestDAVRcloneVFSNestedMedia(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("rclone is not installed")
	}
	d, _, _ := davFake(t)
	dav := httptest.NewServer(d)
	defer dav.Close()
	config := filepath.Join(t.TempDir(), "rclone.conf")
	if err := os.WriteFile(config, []byte("[catalog]\ntype = webdav\nurl = "+dav.URL+"/dav/\nvendor = other\n"), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "rclone", "--config", config, "serve", "http", "catalog:", "--addr", address, "--read-only")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	client := &http.Client{Timeout: time.Second}
	for start := time.Now(); ; {
		resp, err := client.Get("http://" + address + "/")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Since(start) > 5*time.Second {
			t.Fatalf("rclone HTTP server did not start: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := client.Get("http://" + address + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, string(body)
	}
	root := "/catalog/latest/0001-SSIS-006/"
	code, playlist := get(root + "preview.m3u8")
	if code != 200 {
		t.Fatalf("rclone VFS preview: %d %s", code, playlist)
	}
	child := regexp.MustCompile(`media/[a-f0-9]{32}\.m3u8`).FindString(playlist)
	code, nested := get(root + child)
	if code != 200 {
		t.Fatalf("rclone VFS child: %d %s", code, nested)
	}
	segment := regexp.MustCompile(`[a-f0-9]{32}\.ts`).FindString(nested)
	if segment == "" {
		t.Fatal("rclone VFS child has no segment")
	}
	code, body := get(root + "media/" + segment)
	if code != 200 || body != "segment data" {
		t.Fatalf("rclone VFS nested segment: %d %q", code, body)
	}
}

func TestDAVAssemblyRoute(t *testing.T) {
	cfg := Config{Plugins: &PluginConfig{Sources: []SourceConfig{{ID: "catalog", Type: "javdb"}}}}
	h := Handler(cfg, nil)
	t.Cleanup(h.Close)
	if w := davRequest(t, h, "PROPFIND", "/dav/", "1", ""); w.Code != 207 || !strings.Contains(w.Body.String(), "catalog") {
		t.Fatalf("assembled DAV route: %d %s", w.Code, w.Body.String())
	}
	if w := davRequest(t, h, "PUT", "/dav/catalog/", "", ""); w.Code != 403 {
		t.Fatalf("assembled write route: %d", w.Code)
	}
	if w := davRequest(t, h, "GET", "/healthz", "", ""); w.Code != 200 {
		t.Fatalf("health route: %d", w.Code)
	}
	if w := davRequest(t, h, "GET", "/dav", "", ""); w.Code != 301 || w.Header().Get("Location") != "/dav/" {
		t.Fatalf("DAV root redirect: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := davRequest(t, h, "OPTIONS", "/dav/", "", ""); w.Code != 204 || !strings.Contains(w.Header().Get("Allow"), "PROPFIND") {
		t.Fatalf("DAV OPTIONS: %d %q", w.Code, w.Header().Get("Allow"))
	}
}

func TestDAVCodePathsAndFallbacks(t *testing.T) {
	d, f, _ := davFake(t)
	f.items = []core.Item{
		{ID: "G8xag", Code: "SSIS-006"},
		{ID: "DupA", Code: "DUP-001"}, {ID: "DupB", Code: "DUP-001"},
		{ID: "NoCode"}, {ID: "Unsafe", Code: "BAD/001"},
		{ID: "Clash", Code: "OtherID"}, {ID: "OtherID", Code: "OTHER-002"},
	}
	f.others = map[string]javdb.Media{}
	for _, item := range f.items[1:] {
		f.others[item.ID] = javdb.Media{ID: item.ID, Number: item.Code}
	}
	for _, base := range []string{"/dav/catalog/latest/", "/dav/catalog/search/SSIS/"} {
		listed := davRequest(t, d, "PROPFIND", base, "1", "")
		if listed.Code != 207 {
			t.Fatalf("page listing %s: %d %s", base, listed.Code, listed.Body.String())
		}
		for _, name := range []string{"0001-SSIS-006", "0002-DupA", "0003-DupB", "0004-NoCode", "0005-Unsafe", "0006-Clash", "0007-OTHER-002"} {
			if !strings.Contains(listed.Body.String(), base+name+"/") {
				t.Fatalf("missing listed path %s%s: %s", base, name, listed.Body.String())
			}
		}
		if strings.Contains(listed.Body.String(), base+"0002-DUP-001/") || strings.Contains(listed.Body.String(), base+"0006-OtherID/") {
			t.Fatalf("unsafe or colliding code listed: %s", listed.Body.String())
		}
		metadata := davRequest(t, d, "GET", base+"0001-SSIS-006/metadata.json", "", "")
		if metadata.Code != 200 || !strings.Contains(metadata.Body.String(), `"id": "G8xag"`) {
			t.Fatalf("code metadata %s: %d %s", base, metadata.Code, metadata.Body.String())
		}
		if oldID := davRequest(t, d, "GET", base+"G8xag/metadata.json", "", ""); oldID.Code != 404 {
			t.Fatalf("old ID alias %s: %d", base, oldID.Code)
		}
		if stale := davRequest(t, d, "GET", base+"MISSING-999/metadata.json", "", ""); stale.Code != 404 {
			t.Fatalf("unlisted code path silently resolved: %d", stale.Code)
		}
	}
	if direct := davRequest(t, d, "GET", "/dav/catalog/items/G8xag/metadata.json", "", ""); direct.Code != 404 {
		t.Fatalf("old items route: %d", direct.Code)
	}
	root := "/dav/catalog/latest/0001-SSIS-006/"
	if cover := davRequest(t, d, "GET", root+"cover.jpg", "", "bytes=0-2"); cover.Code != 206 || !bytes.Equal(cover.Body.Bytes(), []byte{0xff, 0xd8, 0xff}) {
		t.Fatalf("code-path cover: %d %x", cover.Code, cover.Body.Bytes())
	}
	playlist := davRequest(t, d, "GET", root+"preview.m3u8", "", "")
	child := regexp.MustCompile(`media/[a-f0-9]{32}\.m3u8`).FindString(playlist.Body.String())
	if playlist.Code != 200 || child == "" {
		t.Fatalf("code-path playlist: %d %s", playlist.Code, playlist.Body.String())
	}
	if nested := davRequest(t, d, "GET", root+child, "", ""); nested.Code != 200 || !strings.Contains(nested.Body.String(), ".ts") {
		t.Fatalf("code-path nested playlist: %d %s", nested.Code, nested.Body.String())
	}
}

func TestDAVExpandedBrowseTree(t *testing.T) {
	d, _, _ := davFake(t)
	root := "/dav/catalog/"
	if listing := davRequest(t, d, "PROPFIND", root, "1", ""); listing.Code != 207 || !strings.Contains(listing.Body.String(), "categories/") || !strings.Contains(listing.Body.String(), "rankings/") || !strings.Contains(listing.Body.String(), "actors/") || !strings.Contains(listing.Body.String(), "series/") || !strings.Contains(listing.Body.String(), "makers/") {
		t.Fatalf("expanded root: %d %s", listing.Code, listing.Body.String())
	}
	paths := []string{
		"categories/censored/",
		"rankings/playback/all/daily/",
		"rankings/score/western/weekly/",
		"rankings/top250/all/",
		"actors/censored/0001-" + url.PathEscape(davReadableName("演员 A/B", "actor1")) + "/",
		"series/western/0001-" + url.PathEscape(davReadableName("演员 A/B", "actor1")) + "/",
		"makers/fc2/0001-" + url.PathEscape(davReadableName("演员 A/B", "actor1")) + "/",
		"categories/censored/tags/" + url.PathEscape(davReadableName("类别", "main")) + "/" + url.PathEscape(davReadableName("剧情", "123")) + "/",
	}
	for _, browsePath := range paths {
		components := strings.Split(strings.Trim(browsePath, "/"), "/")
		prefix := root
		for _, component := range components {
			parent := davRequest(t, d, "PROPFIND", prefix, "1", "")
			if parent.Code != 207 || !strings.Contains(parent.Body.String(), component+"/") {
				t.Fatalf("browse walk %s -> %s: %d %s", prefix, component, parent.Code, parent.Body.String())
			}
			prefix += component + "/"
		}
		base := root + browsePath
		listed := davRequest(t, d, "PROPFIND", base, "1", "")
		if listed.Code != 207 || !strings.Contains(listed.Body.String(), "0001-SSIS-006/") {
			t.Fatalf("browse listing %s: %d %s", base, listed.Code, listed.Body.String())
		}
		metadata := davRequest(t, d, "GET", base+"0001-SSIS-006/metadata.json", "", "")
		if metadata.Code != 200 || !strings.Contains(metadata.Body.String(), `"id": "G8xag"`) {
			t.Fatalf("browse media %s: %d %s", base, metadata.Code, metadata.Body.String())
		}
	}
}

func TestDAVFlatBrowseAndPlaybackFilters(t *testing.T) {
	d, _, _ := davFake(t)
	for _, path := range []string{"/dav/catalog/latest/", "/dav/catalog/categories/censored/", "/dav/catalog/rankings/top250/all/", "/dav/catalog/rankings/playback/all/daily/"} {
		listing := davRequest(t, d, "PROPFIND", path, "1", "")
		if listing.Code != 207 || !strings.Contains(listing.Body.String(), "0001-SSIS-006/") || strings.Contains(listing.Body.String(), "page-01/") {
			t.Fatalf("flat listing %s: %d %s", path, listing.Code, listing.Body.String())
		}
	}
	for _, path := range []string{"/dav/catalog/categories/censored/page-01/", "/dav/catalog/latest/page-01/", "/dav/catalog/rankings/playback/censored/"} {
		if got := davRequest(t, d, "PROPFIND", path, "0", ""); got.Code != 404 {
			t.Fatalf("invalid browse path %s: %d", path, got.Code)
		}
	}
	filters := davRequest(t, d, "PROPFIND", "/dav/catalog/rankings/playback/", "1", "")
	if filters.Code != 207 || !strings.Contains(filters.Body.String(), "high_score/") || strings.Contains(filters.Body.String(), "censored/") {
		t.Fatalf("playback filters: %d %s", filters.Code, filters.Body.String())
	}
}

func TestDAVFlatPagesMetadataAndRefresh(t *testing.T) {
	d, f, _ := davFake(t)
	d.startedAt = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	f.pages = map[int]core.Page{
		1: {Items: []core.Item{{ID: "G8xag", Code: "SSIS-006", ReleaseDate: "2024-03-04"}}, NextCursor: "2"},
		2: {Items: []core.Item{{ID: "G8xag", Code: "SSIS-006"}, {ID: "B2", Code: "ABP-002", ReleaseDate: "2020-01-02"}}},
	}
	f.others = map[string]javdb.Media{"B2": {ID: "B2", Number: "ABP-002", Title: "Other"}}
	base := "/dav/catalog/latest/"
	listing := davRequest(t, d, "PROPFIND", base, "1", "")
	if listing.Code != 207 || strings.Count(listing.Body.String(), "0001-SSIS-006/") != 1 || !strings.Contains(listing.Body.String(), "0002-ABP-002/") {
		t.Fatalf("merged listing: %d %s", listing.Code, listing.Body.String())
	}
	if got := davRequest(t, d, "GET", base+"0002-ABP-002/metadata.json", "", ""); got.Code != 200 || !strings.Contains(got.Body.String(), `"id": "B2"`) {
		t.Fatalf("second page metadata: %d %s", got.Code, got.Body.String())
	}
	film := davRequest(t, d, "PROPFIND", base+"0002-ABP-002/", "0", "")
	if film.Code != 207 || !strings.Contains(film.Body.String(), "Thu, 02 Jan 2020 00:00:00 GMT") {
		t.Fatalf("movie release date mtime: %d %s", film.Code, film.Body.String())
	}
	old := davRequest(t, d, "PROPFIND", base, "0", "")
	if old.Code != 207 {
		t.Fatalf("parent stat: %d", old.Code)
	}
	parent := davRequest(t, d, "PROPFIND", "/dav/catalog/", "1", "")
	davTime := regexp.MustCompile(`<D:getlastmodified>[^<]+</D:getlastmodified>`)
	parentNode := regexp.MustCompile(`<D:response><D:href>/dav/catalog/latest/</D:href>(.*?)</D:response>`).FindString(parent.Body.String())
	if got, want := davTime.FindString(parentNode), davTime.FindString(old.Body.String()); got == "" || got != want {
		t.Fatalf("loaded parent child mtime %q differs from direct stat %q", got, want)
	}
	f.pages[2] = core.Page{Items: []core.Item{{ID: "C3", Code: "ABP-003"}}}
	f.others["C3"] = javdb.Media{ID: "C3", Number: "ABP-003", Title: "Third"}
	still := davRequest(t, d, "PROPFIND", base, "1", "")
	if !strings.Contains(still.Body.String(), "0002-ABP-002/") {
		t.Fatal("cached listing changed before refresh")
	}
	d.flat.mu.Lock()
	d.flat.entries["movies:/catalog/latest"].expiresAt = time.Now().Add(-time.Second)
	d.flat.entries["movies:/catalog/latest"].fetchedAt = time.Now().Add(-flatMaxAge)
	d.flat.mu.Unlock()
	time.Sleep(time.Second) // DAV modification times have one-second precision.
	fresh := davRequest(t, d, "PROPFIND", base, "1", "")
	if fresh.Code != 207 || !strings.Contains(fresh.Body.String(), "0002-ABP-003/") || strings.Contains(fresh.Body.String(), "0002-ABP-002/") {
		t.Fatalf("refreshed listing: %d %s", fresh.Code, fresh.Body.String())
	}
	newStat := davRequest(t, d, "PROPFIND", base, "0", "")
	if old.Body.String() == newStat.Body.String() {
		t.Fatal("structural directory mtime did not refresh")
	}
	parent = davRequest(t, d, "PROPFIND", "/dav/catalog/", "1", "")
	parentNode = regexp.MustCompile(`<D:response><D:href>/dav/catalog/latest/</D:href>(.*?)</D:response>`).FindString(parent.Body.String())
	if got, want := davTime.FindString(parentNode), davTime.FindString(newStat.Body.String()); got == "" || got != want {
		t.Fatalf("refreshed parent child mtime %q differs from direct stat %q", got, want)
	}
}

func TestDAVFlatListingRejectsOversizedInput(t *testing.T) {
	d, _, _ := davFake(t)
	score := strings.Repeat("x", 1<<20)
	_, err := d.flatMovies(context.Background(), "/catalog/latest", 10, func(_ context.Context, page int) (core.Page, error) {
		start := (page - 1) * 3
		count := min(3, 17-start)
		items := make([]core.Item, count)
		for i := range items {
			items[i] = core.Item{ID: fmt.Sprintf("ID%d", start+i), Score: score}
		}
		next := ""
		if start+count < 17 {
			next = strconv.Itoa(page + 1)
		}
		return core.Page{Items: items, NextCursor: next}, nil
	})
	if err != errFlatListLimit {
		t.Fatalf("oversized upstream listing accepted: %v", err)
	}
}

func TestDAVEntityTokenSurvivesDirectoryRefresh(t *testing.T) {
	d, f, _ := davFake(t)
	actor := javdb.BrowseEntity{ID: "actor1", Name: "演员 A/B"}
	f.directories = map[int][]javdb.BrowseEntity{1: {actor}}
	base := "/dav/catalog/actors/censored/" + url.PathEscape("0001-"+davReadableName(actor.Name, actor.ID)) + "/0001-SSIS-006/"
	preview := davRequest(t, d, "GET", base+"preview.m3u8", "", "")
	child := regexp.MustCompile(`media/[a-f0-9]{32}\.m3u8`).FindString(preview.Body.String())
	if preview.Code != 200 || child == "" {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	f.directories[1] = []javdb.BrowseEntity{{ID: "newactor", Name: "First"}, actor}
	d.flat.mu.Lock()
	d.flat.entries["entities:/catalog/actors/censored"].expiresAt = time.Now().Add(-time.Second)
	d.flat.mu.Unlock()
	if got := davRequest(t, d, "GET", base+child, "", ""); got.Code != 200 {
		t.Fatalf("unexpired playlist token after reorder: %d %s", got.Code, got.Body.String())
	}
}

func TestDAVNestedDirectoryMtimeUsesLoadedChildSnapshot(t *testing.T) {
	for _, tc := range []struct{ parent, child, key string }{
		{"/catalog/actors/censored", "0001-" + davReadableName("演员 A/B", "actor1"), "entities:/catalog/actors/censored"},
		{"/catalog/categories/censored", "tags", "movies:/catalog/categories/censored"},
		{"/catalog/categories/censored/tags/" + davReadableName("类别", "main"), davReadableName("剧情", "123"), "tags:/catalog/categories/censored/tags"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			d, _, _ := davFake(t)
			ctx := context.Background()
			parent, err := d.lookup(ctx, tc.parent)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.children(ctx, tc.parent, parent); err != nil {
				t.Fatal(err)
			}
			old := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
			d.flat.mu.Lock()
			entry := d.flat.entries[tc.key]
			entry.fetchedAt = old
			for i := range entry.nodes {
				entry.nodes[i].modTime = old
			}
			d.flat.mu.Unlock()
			child, err := d.lookup(ctx, tc.parent+"/"+tc.child)
			if err != nil {
				t.Fatal(err)
			}
			listed, err := d.children(ctx, tc.parent, parent)
			if err != nil {
				t.Fatal(err)
			}
			for _, node := range listed {
				if node.name == tc.child {
					if !node.modTime.Equal(child.modTime) {
						t.Fatalf("listed mtime %s differs from direct %s", node.modTime, child.modTime)
					}
					return
				}
			}
			t.Fatal("child missing")
		})
	}
}

func TestDAVFlatListingFailureIsAtomic(t *testing.T) {
	d, f, _ := davFake(t)
	f.pages = map[int]core.Page{1: {Items: []core.Item{{ID: "G8xag", Code: "SSIS-006"}}, NextCursor: "2"}}
	f.failPage = 2
	if got := davRequest(t, d, "PROPFIND", "/dav/catalog/latest/", "1", ""); got.Code != 502 || strings.Contains(got.Body.String(), "SSIS-006/") {
		t.Fatalf("partial listing leaked: %d %s", got.Code, got.Body.String())
	}
	f.failPage = 0
	if got := davRequest(t, d, "PROPFIND", "/dav/catalog/latest/", "1", ""); got.Code != 207 || !strings.Contains(got.Body.String(), "0001-SSIS-006/") {
		t.Fatalf("recovery after failed refresh: %d %s", got.Code, got.Body.String())
	}
}

func TestDAVMetadataETagDistinguishesSameLengthBodies(t *testing.T) {
	d, f, _ := davFake(t)
	f.items = []core.Item{{ID: "G8xag", Code: "AA-001"}, {ID: "A8xag", Code: "BB-002"}}
	f.media.Number = "AA-001"
	f.others = map[string]javdb.Media{"A8xag": {ID: "A8xag", Number: "BB-002", Title: "Title", Summary: "Summary"}}
	paths := []string{"/dav/catalog/latest/0001-AA-001/metadata.json", "/dav/catalog/latest/0002-BB-002/metadata.json"}
	var etags []string
	for _, path := range paths {
		get := davRequest(t, d, "GET", path, "", "")
		head := davRequest(t, d, "HEAD", path, "", "")
		prop := davRequest(t, d, "PROPFIND", path, "0", "")
		etag := get.Header().Get("ETag")
		if get.Code != 200 || head.Code != 200 || prop.Code != 207 || etag == "" || head.Header().Get("ETag") != etag || !strings.Contains(prop.Body.String(), "<D:getetag>"+etag+"</D:getetag>") || get.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("metadata validators %s: GET=%d HEAD=%d PROP=%d ETag=%q", path, get.Code, head.Code, prop.Code, etag)
		}
		conditional := httptest.NewRequest("GET", path, nil)
		conditional.Header.Set("If-None-Match", etag)
		w := httptest.NewRecorder()
		d.ServeHTTP(w, conditional)
		if w.Code != 304 {
			t.Fatalf("same metadata conditional: %d", w.Code)
		}
		etags = append(etags, etag)
	}
	if etags[0] == etags[1] {
		t.Fatal("distinct metadata used the same ETag")
	}
}

func TestDAVFileDatesAndContentValidators(t *testing.T) {
	d, f, _ := davFake(t)
	f.items = []core.Item{{ID: f.media.ID, Code: f.media.Number, ReleaseDate: "2020-01-02"}}
	f.media.ReleaseDate = "2020-01-02"
	original := d.client.Transport
	d.client.Transport = davRoundTrip(func(r *http.Request) (*http.Response, error) {
		resp, err := original.RoundTrip(r)
		if err != nil || r.URL.Path != "/sample.jpg" || r.Method == http.MethodHead {
			return resp, err
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(data) > 10 {
			data[10] ^= 1
		}
		resp.Body = io.NopCloser(bytes.NewReader(data))
		return resp, nil
	})
	base := "/dav/catalog/latest/0001-SSIS-006/"
	date := "Thu, 02 Jan 2020 00:00:00 GMT"
	var etags []string
	for _, file := range []string{"metadata.json", "cover.jpg", "images/01.jpg"} {
		path := base + file
		prop := davRequest(t, d, "PROPFIND", path, "0", "")
		get := davRequest(t, d, "GET", path, "", "")
		head := davRequest(t, d, "HEAD", path, "", "")
		propertyETag := regexp.MustCompile(`<D:getetag>([^<]+)</D:getetag>`).FindStringSubmatch(prop.Body.String())
		if prop.Code != 207 || !strings.Contains(prop.Body.String(), date) || len(propertyETag) != 2 || propertyETag[1] != get.Header().Get("ETag") || get.Code != 200 || head.Code != 200 || get.Header().Get("Last-Modified") != date || head.Header().Get("Last-Modified") != date || get.Header().Get("ETag") == "" || head.Header().Get("ETag") != get.Header().Get("ETag") {
			t.Fatalf("file date and ETag %s: PROP=%d GET=%d HEAD=%d LastModified=%q ETag=%q", file, prop.Code, get.Code, head.Code, get.Header().Get("Last-Modified"), get.Header().Get("ETag"))
		}
		request := httptest.NewRequest("GET", path, nil)
		request.Header.Set("If-Modified-Since", "Wed, 01 Jan 2031 00:00:00 GMT")
		conditional := httptest.NewRecorder()
		d.ServeHTTP(conditional, request)
		if conditional.Code != 200 {
			t.Fatalf("historical date caused false 304 for %s: %d", file, conditional.Code)
		}
		etags = append(etags, get.Header().Get("ETag"))
	}
	if etags[1] == etags[2] {
		t.Fatal("same-length decoded images used the same content ETag")
	}
	request := httptest.NewRequest("GET", base+"metadata.json", nil)
	request.Header.Set("Range", "bytes=0-4")
	request.Header.Set("If-Range", date)
	response := httptest.NewRecorder()
	d.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("historical date If-Range returned partial content: %d", response.Code)
	}
	request = httptest.NewRequest("GET", base+"metadata.json", nil)
	request.Header.Set("Range", "bytes=0-4")
	request.Header.Set("If-Range", etags[0])
	response = httptest.NewRecorder()
	d.ServeHTTP(response, request)
	if response.Code != 206 {
		t.Fatalf("content ETag If-Range rejected: %d", response.Code)
	}
	f.items[0].ReleaseDate = "2099-01-01"
	f.media.ReleaseDate = "2099-01-01"
	d.flat.mu.Lock()
	d.flat.entries["movies:/catalog/latest"].expiresAt = time.Now().Add(-time.Second)
	d.flat.entries["movies:/catalog/latest"].fetchedAt = time.Now().Add(-flatMaxAge)
	d.flat.mu.Unlock()
	future := davRequest(t, d, "GET", base+"metadata.json", "", "")
	if future.Header().Get("Last-Modified") != "" {
		t.Fatalf("future date sent as HTTP Last-Modified: %q", future.Header().Get("Last-Modified"))
	}
}

func TestDAVImagePropertyETagTracksSameSizeChanges(t *testing.T) {
	d, f, _ := davFake(t)
	f.items = []core.Item{{ID: f.media.ID, Code: f.media.Number, ReleaseDate: "2020-01-02"}}
	path := "/dav/catalog/latest/0001-SSIS-006/cover.jpg"
	propertyETag := func() string {
		t.Helper()
		prop := davRequest(t, d, "PROPFIND", path, "0", "")
		match := regexp.MustCompile(`<D:getetag>([^<]+)</D:getetag>`).FindStringSubmatch(prop.Body.String())
		if prop.Code != 207 || len(match) != 2 {
			t.Fatalf("image property ETag: %d %s", prop.Code, prop.Body.String())
		}
		return match[1]
	}
	first := davRequest(t, d, "GET", path, "", "")
	firstTag := propertyETag()
	if firstTag != first.Header().Get("ETag") {
		t.Fatalf("image property/GET ETags differ: %q %q", firstTag, first.Header().Get("ETag"))
	}
	original := d.client.Transport
	d.client.Transport = davRoundTrip(func(r *http.Request) (*http.Response, error) {
		resp, err := original.RoundTrip(r)
		if err != nil || r.URL.Path != "/cover.jpg" || r.Method == http.MethodHead {
			return resp, err
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(data) > 10 {
			data[10] ^= 1
		}
		resp.Body = io.NopCloser(bytes.NewReader(data))
		return resp, nil
	})
	changed := davRequest(t, d, "GET", path, "", "")
	changedTag := propertyETag()
	if len(changed.Body.Bytes()) != len(first.Body.Bytes()) || bytes.Equal(changed.Body.Bytes(), first.Body.Bytes()) || changedTag == firstTag || changedTag != changed.Header().Get("ETag") {
		t.Fatalf("same-size image mutation not visible in DAV validator: first=%q changed=%q", firstTag, changedTag)
	}
}

func TestDAVRcloneClient(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("rclone is not installed")
	}
	d, _, _ := davFake(t)
	srv := httptest.NewServer(d)
	defer srv.Close()
	config := filepath.Join(t.TempDir(), "rclone.conf")
	content := "[catalog]\ntype = webdav\nurl = " + srv.URL + "/dav/\nvendor = other\n"
	if err := os.WriteFile(config, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "rclone", append([]string{"--config", config}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("rclone %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	if out := run("lsf", "catalog:catalog/latest"); !strings.Contains(out, "SSIS-006/") {
		t.Fatalf("rclone lsf: %s", out)
	}
	for _, step := range []struct{ path, child string }{
		{"catalog:catalog", "categories/"},
		{"catalog:catalog/categories", "censored/"},
		{"catalog:catalog/categories/censored", "0001-SSIS-006/"},
		{"catalog:catalog/rankings", "playback/"},
		{"catalog:catalog/rankings/playback", "high_score/"},
		{"catalog:catalog/actors/censored", "0001-" + davReadableName("演员 A/B", "actor1") + "/"},
	} {
		if out := run("lsf", step.path); !strings.Contains(out, step.child) {
			t.Fatalf("rclone browse walk %s lacks %s: %s", step.path, step.child, out)
		}
	}
	if out := run("--webdav-url", srv.URL+"/dav/", "--webdav-vendor", "other", "lsf", ":webdav:catalog/latest"); !strings.Contains(out, "0001-SSIS-006/") {
		t.Fatalf("rclone ad hoc DAV: %s", out)
	}
	if out := run("cat", "catalog:catalog/latest/0001-SSIS-006/metadata.json"); !strings.Contains(out, "SSIS-006") {
		t.Fatalf("rclone cat: %s", out)
	}
	if out := run("lsf", "catalog:catalog/categories/censored"); !strings.Contains(out, "SSIS-006/") {
		t.Fatalf("rclone category listing: %s", out)
	}
	if out := run("cat", "catalog:catalog/categories/censored/0001-SSIS-006/metadata.json"); !strings.Contains(out, `"id": "G8xag"`) {
		t.Fatalf("rclone category metadata: %s", out)
	}
	actorName := davReadableName("演员 A/B", "actor1")
	if out := run("lsf", "catalog:catalog/actors/censored"); !strings.Contains(out, actorName+"/") {
		t.Fatalf("rclone actor listing: %s", out)
	}
	if out := run("cat", "catalog:catalog/actors/censored/0001-"+actorName+"/0001-SSIS-006/metadata.json"); !strings.Contains(out, `"id": "G8xag"`) {
		t.Fatalf("rclone Unicode actor traversal: %s", out)
	}
	group := davReadableName("类别", "main")
	tag := davReadableName("剧情", "123")
	if out := run("cat", "catalog:catalog/categories/censored/tags/"+group+"/"+tag+"/0001-SSIS-006/metadata.json"); !strings.Contains(out, `"id": "G8xag"`) {
		t.Fatalf("rclone Unicode tag traversal: %s", out)
	}
	if out := run("cat", "catalog:catalog/latest/0001-SSIS-006/cover.jpg"); !bytes.HasPrefix([]byte(out), []byte{0xff, 0xd8, 0xff}) {
		t.Fatalf("rclone decoded image: %x", out)
	}
	if out := run("rc", "--loopback", "operations/list", "fs=catalog:", "remote=catalog/latest"); !strings.Contains(out, "SSIS-006") {
		t.Fatalf("rclone rc list: %s", out)
	}
	base := "catalog:catalog/latest/0001-SSIS-006/"
	playlist := run("cat", base+"preview.m3u8")
	key := regexp.MustCompile(`media/[a-f0-9]{32}\.bin`).FindString(playlist)
	child := regexp.MustCompile(`media/[a-f0-9]{32}\.m3u8`).FindString(playlist)
	if key == "" || child == "" {
		t.Fatalf("rclone playlist: %s", playlist)
	}
	if out := run("cat", base+key); out != "0123456789abcdef" {
		t.Fatalf("rclone HLS key: %q", out)
	}
	childData := run("cat", base+child)
	segment := regexp.MustCompile(`[a-f0-9]{32}\.ts`).FindString(childData)
	if segment == "" {
		t.Fatalf("rclone nested playlist: %s", childData)
	}
	if out := run("cat", base+"media/"+segment); out != "segment data" {
		t.Fatalf("rclone HLS segment: %q", out)
	}
}
