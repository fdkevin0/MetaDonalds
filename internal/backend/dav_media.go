package backend

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/plugins/javdb"
	tracepkg "github.com/fdkevin0/MetaDonalds/internal/trace"
)

const maxImage = 16 << 20
const maxPlaylist = 1 << 20
const defaultMediaIdleTimeout = 30 * time.Second

type mediaVariant struct{ url, kind string }
type mediaToken struct {
	url      string
	parent   string
	playlist bool
	expires  time.Time
	modTime  time.Time
}
type mediaTokens struct {
	mu      sync.Mutex
	entries map[string]mediaToken
	byURL   map[string]string
}

type playlistEntry struct {
	data    []byte
	expires time.Time
}
type playlistCache struct {
	mu      sync.Mutex
	entries map[string]playlistEntry
}

func newPlaylistCache() *playlistCache { return &playlistCache{entries: map[string]playlistEntry{}} }
func (c *playlistCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.entries[key]
	if !ok || time.Now().After(v.expires) {
		delete(c.entries, key)
		return nil, false
	}
	return v.data, true
}
func (c *playlistCache) put(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.entries {
		if time.Now().After(v.expires) {
			delete(c.entries, k)
		}
	}
	if len(c.entries) >= 32 {
		var oldest string
		var when time.Time
		for k, v := range c.entries {
			if oldest == "" || v.expires.Before(when) {
				oldest, when = k, v.expires
			}
		}
		delete(c.entries, oldest)
	}
	c.entries[key] = playlistEntry{data, time.Now().Add(5 * time.Minute)}
}

func newMediaTokens() *mediaTokens {
	return &mediaTokens{entries: map[string]mediaToken{}, byURL: map[string]string{}}
}

var errExpiredToken = errors.New("media link expired")
var errMediaExpired = errors.New("upstream media link expired")
var errTokenCapacity = errors.New("too many active media links")
var tokenExt = regexp.MustCompile(`^\.[a-zA-Z0-9]+$`)

func (t *mediaTokens) add(raw, parent string, playlist bool) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key, _, err := t.addLocked(raw, parent, playlist)
	return key, err
}

// addLocked is also used by playlist rewriting, which holds the lock until its
// whole batch succeeds or rolls back newly allocated links.
func (t *mediaTokens) addLocked(raw, parent string, playlist bool, ownerTime ...time.Time) (string, bool, error) {
	keyForURL := parent + "\x00" + raw
	now := time.Now()
	if key := t.byURL[keyForURL]; key != "" {
		v := t.entries[key]
		if now.Before(v.expires) {
			v.expires = now.Add(4 * time.Hour)
			if len(ownerTime) > 0 && !ownerTime[0].IsZero() {
				v.modTime = ownerTime[0]
			}
			t.entries[key] = v
			return key, false, nil
		}
		delete(t.byURL, keyForURL)
		delete(t.entries, key)
	}
	if len(t.entries) >= 10000 {
		for key, v := range t.entries {
			if !now.Before(v.expires) {
				delete(t.byURL, v.parent+"\x00"+v.url)
				delete(t.entries, key)
			}
		}
		if len(t.entries) >= 10000 {
			return "", false, errTokenCapacity
		}
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", false, err
	}
	ext := path.Ext(strings.Split(raw, "?")[0])
	if len(ext) > 8 || !tokenExt.MatchString(ext) {
		ext = ""
	}
	key := hex.EncodeToString(b[:]) + ext
	var modTime time.Time
	if len(ownerTime) > 0 {
		modTime = ownerTime[0]
	}
	t.entries[key] = mediaToken{url: raw, parent: parent, playlist: playlist, expires: now.Add(4 * time.Hour), modTime: modTime}
	t.byURL[keyForURL] = key
	return key, true, nil
}

func (t *mediaTokens) get(key, parent string) (mediaToken, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.entries[key]
	if !ok || v.parent != parent {
		return mediaToken{}, os.ErrNotExist
	}
	if time.Now().After(v.expires) {
		return mediaToken{}, errExpiredToken
	}
	v.expires = time.Now().Add(4 * time.Hour)
	t.entries[key] = v
	return v, nil
}

func (t *mediaTokens) list(parent string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []string{}
	for key, v := range t.entries {
		if v.parent == parent && time.Now().Before(v.expires) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func safeMediaClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{Proxy: nil, ResponseHeaderTimeout: 20 * time.Second, IdleConnTimeout: 30 * time.Second, MaxIdleConns: 32}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !publicIP(ip.IP) {
				continue
			}
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, errors.New("media host has no reachable public address")
	}
	return &http.Client{Transport: transport, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many media redirects")
		}
		return validateMediaURL(r.URL.String())
	}}
}

func publicIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	return a.IsGlobalUnicast() && !a.IsPrivate() && !netip.MustParsePrefix("100.64.0.0/10").Contains(a)
}

func validateMediaURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("invalid media URL")
	}
	if strings.EqualFold(u.Hostname(), "localhost") || strings.HasSuffix(strings.ToLower(u.Hostname()), ".localhost") {
		return errors.New("private media host")
	}
	if u.Port() != "" && !(u.Scheme == "https" && u.Port() == "443" || u.Scheme == "http" && u.Port() == "80") {
		return errors.New("media URL uses nonstandard port")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !publicIP(ip) {
		return errors.New("private media address")
	}
	return nil
}

func (d *davServer) request(ctx context.Context, method, raw string, headers http.Header) (*http.Response, error) {
	ctx, span := tracepkg.Begin(ctx, "media.http")
	host, endpoint := mediaTraceTarget(raw)
	fields := tracepkg.Fields{Outcome: "ok", Method: method, Host: host, Endpoint: endpoint}
	if err := validateMediaURL(raw); err != nil {
		fields.Outcome, fields.ErrorClass = "error", "invalid_url"
		span.End(fields)
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, raw, nil)
	if err != nil {
		fields.Outcome, fields.ErrorClass = "error", "request"
		span.End(fields)
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	for k, values := range headers {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
	resp, err := d.client.Do(req)
	if err != nil {
		fields.Outcome, fields.ErrorClass = "error", "network"
		span.End(fields)
		return nil, err
	}
	fields.Status = resp.StatusCode
	resp.Body = &tracedBody{ReadCloser: resp.Body, span: span, fields: fields}
	return resp, nil
}

type tracedBody struct {
	io.ReadCloser
	span    *tracepkg.Span
	fields  tracepkg.Fields
	readErr bool
}

func (b *tracedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.fields.Bytes += int64(n)
	if err != nil && !errors.Is(err, io.EOF) {
		b.readErr = true
	}
	return n, err
}

func (b *tracedBody) Close() error {
	err := b.ReadCloser.Close()
	if b.readErr || err != nil {
		b.fields.Outcome, b.fields.ErrorClass = "error", "read"
	} else if b.fields.Status >= 400 {
		b.fields.Outcome, b.fields.ErrorClass = "error", "http_status"
	}
	b.span.End(b.fields)
	return err
}

var safeLoggedHost = regexp.MustCompile(`^[A-Za-z0-9.:-]{1,253}$`)

func mediaTraceTarget(raw string) (host, endpoint string) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "media"
	}
	host = u.Hostname()
	if !safeLoggedHost.MatchString(host) {
		host = "other"
	}
	switch {
	case u.Path == "/api/v/resolve":
		endpoint = "resolver"
	case strings.HasSuffix(u.Path, ".m3u8"):
		endpoint = "playlist"
	case strings.HasSuffix(u.Path, ".mp4"):
		endpoint = "video"
	case strings.HasSuffix(u.Path, ".ts"):
		endpoint = "segment"
	case strings.HasSuffix(u.Path, ".jpg") || strings.HasSuffix(u.Path, ".jpeg") || strings.HasSuffix(u.Path, ".png"):
		endpoint = "image"
	case strings.HasSuffix(u.Path, ".bin"):
		endpoint = "key"
	default:
		endpoint = "media"
	}
	return host, endpoint
}

func readLimited(resp *http.Response, limit int64) ([]byte, error) {
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusGone {
		return nil, errMediaExpired
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("media HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || int64(len(data)) > limit {
		return nil, errors.New("media response exceeds limit or is empty")
	}
	return data, nil
}

func (d *davServer) resolve(ctx context.Context, m javdb.Media) (out []mediaVariant, err error) {
	ctx, span := tracepkg.Begin(ctx, "dav.resolve")
	defer func() {
		fields := tracepkg.Fields{Outcome: "ok", Count: len(out)}
		if err != nil {
			fields.Outcome, fields.ErrorClass = "error", davFailureCategory(err)
		}
		span.End(fields)
	}()
	types := []string{"av", "uc", "west", "fc2"}
	if m.Type < 0 || m.Type >= len(types) {
		return nil, errors.New("unsupported movie type")
	}
	q := url.Values{"code": {m.Number}, "lang": {"zh"}, "type": {types[m.Type]}}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := d.request(ctx, http.MethodGet, strings.TrimRight(d.resolveBase, "/")+"/api/v/resolve?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := readLimited(resp, 1<<20)
	if err != nil {
		return nil, err
	}
	var result struct {
		Variants []struct {
			SourceURL  string `json:"sourceUrl"`
			SourceType string `json:"sourceType"`
		} `json:"variants"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	for _, v := range result.Variants {
		kind := mediaKind(v.SourceURL, v.SourceType)
		if kind == "" {
			continue
		}
		if strings.HasPrefix(v.SourceURL, "data:") {
			if kind != "playlist" || len(v.SourceURL) > maxPlaylist*3 {
				continue
			}
		} else if validateMediaURL(v.SourceURL) != nil {
			continue
		}
		out = append(out, mediaVariant{v.SourceURL, kind})
		if len(out) == 8 {
			break
		}
	}
	if len(out) == 0 {
		return nil, errors.New("resolve returned no supported online media")
	}
	return out, nil
}

func mediaKind(raw, sourceType string) string {
	if strings.HasPrefix(raw, "data:") {
		if strings.Contains(strings.ToLower(raw[:min(len(raw), 100)]), "mpegurl") {
			return "playlist"
		}
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	p := strings.ToLower(u.Path)
	t := strings.ToLower(sourceType)
	if strings.HasSuffix(p, ".m3u8") || strings.Contains(t, "hls") || strings.Contains(t, "mpegurl") {
		return "playlist"
	}
	if strings.HasSuffix(p, ".mp4") || t == "video/mp4" || t == "mp4" {
		return "proxy"
	}
	return ""
}

func variantName(i int, v mediaVariant) string {
	if v.kind == "playlist" {
		return fmt.Sprintf("video-%d.m3u8", i+1)
	}
	return fmt.Sprintf("video-%d.mp4", i+1)
}

func (d *davServer) variantNode(ctx context.Context, name string, v mediaVariant, parent string, ownerTime ...time.Time) (davNode, error) {
	if v.kind == "playlist" {
		return d.playlistNode(ctx, name, v.url, "media/", parent, ownerTime...)
	}
	size, mime, err := d.remoteSize(ctx, v.url)
	if err != nil {
		return davNode{}, err
	}
	if size < 0 {
		return davNode{}, errors.New("upstream video size unavailable")
	}
	if size < 8 {
		return davNode{}, errors.New("invalid MP4 size")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := d.request(ctx, http.MethodGet, v.url, http.Header{"Range": []string{"bytes=0-31"}})
	if err != nil {
		return davNode{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return davNode{}, errors.New("MP4 source does not support byte ranges")
	}
	first, err := readLimited(resp, 32)
	if err != nil {
		return davNode{}, err
	}
	if len(first) < 8 || string(first[4:8]) != "ftyp" {
		return davNode{}, errors.New("online source is not an MP4")
	}
	if mime == "" {
		mime = "video/mp4"
	}
	var modTime time.Time
	if len(ownerTime) > 0 {
		modTime = ownerTime[0]
	}
	return davNode{name: name, kind: "proxy", mime: mime, url: v.url, size: size, modTime: modTime}, nil
}

func (d *davServer) remoteSize(ctx context.Context, raw string) (int64, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := d.request(ctx, http.MethodHead, raw, nil)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusGone {
			return 0, "", errMediaExpired
		}
		return 0, "", fmt.Errorf("media HEAD HTTP %d", resp.StatusCode)
	}
	return resp.ContentLength, resp.Header.Get("Content-Type"), nil
}

func imageSignature(data []byte) bool {
	return bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}) || bytes.HasPrefix(data, []byte{0x89, 0x50, 0x4e, 0x47}) || bytes.HasPrefix(data, []byte("GIF8")) || bytes.HasPrefix(data, []byte("BM")) || bytes.HasPrefix(data, []byte("RIFF")) && len(data) >= 12 && string(data[8:12]) == "WEBP"
}

func decodeImage(data []byte) ([]byte, string, error) {
	decode := func(start int, key byte) []byte {
		out := make([]byte, len(data)-start)
		for i := range out {
			out[i] = data[i+start] ^ key
		}
		return out
	}
	if !imageSignature(data) {
		if len(data) > 1 && imageSignature(decode(1, data[0])) {
			data = decode(1, data[0])
		} else {
			found := false
			for skip := 0; skip <= 2 && skip < len(data); skip++ {
				candidate := decode(skip, 0x7f)
				if imageSignature(candidate) {
					data = candidate
					found = true
					break
				}
			}
			if !found {
				return nil, "", errors.New("unrecognized encoded image")
			}
		}
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return nil, "", errors.New("decoded image is invalid")
	}
	return data, mime, nil
}

func (d *davServer) imageData(ctx context.Context, raw string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resp, err := d.request(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	data, err := readLimited(resp, maxImage)
	if err != nil {
		return nil, "", err
	}
	_, span := tracepkg.Begin(ctx, "dav.image.decode")
	decoded, mime, err := decodeImage(data)
	fields := tracepkg.Fields{Outcome: "ok", Bytes: int64(len(decoded))}
	if err != nil {
		fields.Outcome, fields.ErrorClass = "error", "invalid_image"
	}
	span.End(fields)
	return decoded, mime, err
}

func (d *davServer) imageDataMemo(ctx context.Context, raw string) ([]byte, string, error) {
	if memo, ok := ctx.Value(davMemoKey{}).(*davMemo); ok {
		if image, found := memo.images[raw]; found {
			return image.data, image.mime, nil
		}
	}
	data, mime, err := d.imageData(ctx, raw)
	if err == nil {
		if memo, ok := ctx.Value(davMemoKey{}).(*davMemo); ok {
			memo.images[raw] = davImage{data: data, mime: mime}
		}
	}
	return data, mime, err
}

func (d *davServer) imageNode(ctx context.Context, name, raw string) (davNode, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ctx, probeSpan := tracepkg.Begin(ctx, "dav.image.probe")
	probeFields := tracepkg.Fields{Outcome: "error", ErrorClass: "upstream"}
	defer func() { probeSpan.End(probeFields) }()
	headers := http.Header{"Range": []string{"bytes=0-31"}}
	resp, err := d.request(ctx, http.MethodGet, raw, headers)
	if err != nil {
		return davNode{}, err
	}
	defer resp.Body.Close()
	data, err := readLimited(resp, maxImage)
	if err != nil {
		return davNode{}, err
	}
	_, decodeSpan := tracepkg.Begin(ctx, "dav.image.decode")
	decoded, mime, err := decodeImage(data)
	fields := tracepkg.Fields{Outcome: "ok", Bytes: int64(len(decoded))}
	if err != nil {
		fields.Outcome, fields.ErrorClass = "error", "invalid_image"
	}
	decodeSpan.End(fields)
	if err != nil {
		probeFields.ErrorClass = "invalid_image"
		return davNode{}, err
	}
	skip := len(data) - len(decoded)
	var total int64
	complete := resp.StatusCode == http.StatusOK
	if resp.StatusCode == http.StatusPartialContent {
		part := resp.Header.Get("Content-Range")
		slash := strings.LastIndexByte(part, '/')
		if slash < 0 {
			return davNode{}, errors.New("image range lacks total size")
		}
		total, err = strconv.ParseInt(part[slash+1:], 10, 64)
		if err != nil {
			return davNode{}, err
		}
		complete = strings.HasPrefix(part, "bytes 0-") && total == int64(len(data))
	} else {
		total = int64(len(data))
	}
	if total < int64(skip) || total > maxImage {
		return davNode{}, errors.New("image size out of bounds")
	}
	if complete {
		if memo, ok := ctx.Value(davMemoKey{}).(*davMemo); ok {
			memo.images[raw] = davImage{data: decoded, mime: mime}
		}
	}
	probeFields = tracepkg.Fields{Outcome: "ok", Bytes: int64(len(decoded))}
	return davNode{name: name, kind: "image", url: raw, mime: mime, size: total - int64(skip)}, nil
}

func (d *davServer) playlistNode(ctx context.Context, name, raw, scope, parent string, ownerTime ...time.Time) (davNode, error) {
	data, err := d.playlistData(ctx, raw, scope, parent, ownerTime...)
	if err != nil {
		return davNode{}, err
	}
	var modTime time.Time
	if len(ownerTime) > 0 {
		modTime = ownerTime[0]
	}
	return davNode{name: name, kind: "playlist", url: raw, scope: scope, parent: parent, mime: "application/vnd.apple.mpegurl", data: data, size: int64(len(data)), modTime: modTime}, nil
}

func (d *davServer) playlistData(ctx context.Context, raw, scope, parent string, ownerTime ...time.Time) ([]byte, error) {
	cacheKey := parent + "\x00" + raw + "\x00" + scope
	_, cacheSpan := tracepkg.Begin(ctx, "dav.playlist.cache")
	if data, ok := d.playlists.get(cacheKey); ok {
		cacheSpan.End(tracepkg.Fields{Outcome: "hit", Cache: "hit", Bytes: int64(len(data))})
		return data, nil
	}
	cacheSpan.End(tracepkg.Fields{Outcome: "miss", Cache: "miss"})
	var data []byte
	var base *url.URL
	if strings.HasPrefix(raw, "data:") {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		comma := strings.IndexByte(u.Opaque, ',')
		if comma < 0 {
			return nil, errors.New("invalid data playlist")
		}
		content := u.Opaque[comma+1:]
		if strings.HasSuffix(strings.ToLower(u.Opaque[:comma]), ";base64") {
			data, err = base64.StdEncoding.DecodeString(content)
		} else {
			var decoded string
			decoded, err = url.PathUnescape(content)
			data = []byte(decoded)
		}
		if err != nil {
			return nil, err
		}
		if len(data) > maxPlaylist {
			return nil, errors.New("playlist too large")
		}
		base, err = url.Parse(strings.TrimRight(d.resolveBase, "/") + "/")
		if err != nil {
			return nil, err
		}
	} else {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		resp, err := d.request(ctx, http.MethodGet, raw, nil)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		data, err = readLimited(resp, maxPlaylist)
		if err != nil {
			return nil, err
		}
		base = resp.Request.URL
	}
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("#EXTM3U")) {
		return nil, errors.New("invalid HLS playlist")
	}
	_, rewriteSpan := tracepkg.Begin(ctx, "dav.hls.rewrite")
	rewritten, err := d.rewritePlaylist(string(data), base, scope, parent, ownerTime...)
	fields := tracepkg.Fields{Outcome: "ok", Bytes: int64(len(rewritten))}
	if err != nil {
		fields.Outcome, fields.ErrorClass = "error", "invalid_playlist"
	}
	rewriteSpan.End(fields)
	if err == nil {
		d.playlists.put(cacheKey, rewritten)
	}
	return rewritten, err
}

var hlsURI = regexp.MustCompile(`URI="([^"]+)"`)

func (d *davServer) rewritePlaylist(body string, base *url.URL, scope, parent string, ownerTime ...time.Time) (rewritten []byte, err error) {
	lines := strings.Split(body, "\n")
	refs := 0
	playable := false
	for _, line := range lines {
		if strings.HasPrefix(line, "#") {
			refs += len(hlsURI.FindAllString(line, -1))
			if strings.HasPrefix(line, "#EXT-X-MEDIA:") || strings.HasPrefix(line, "#EXT-X-I-FRAME-STREAM-INF:") {
				playable = true
			}
		} else if strings.TrimSpace(line) != "" {
			refs++
			playable = true
		}
	}
	if !playable {
		return nil, errors.New("HLS playlist has no media")
	}
	if refs > 2000 {
		return nil, errors.New("playlist has too many media links")
	}
	d.tokens.mu.Lock()
	added := []string{}
	defer func() {
		if err != nil {
			for _, key := range added {
				v := d.tokens.entries[key]
				delete(d.tokens.byURL, v.parent+"\x00"+v.url)
				delete(d.tokens.entries, key)
			}
		}
		d.tokens.mu.Unlock()
	}()
	for i, line := range lines {
		if strings.HasPrefix(line, "#") {
			var rewriteErr error
			lines[i] = hlsURI.ReplaceAllStringFunc(line, func(part string) string {
				if rewriteErr != nil {
					return part
				}
				value := strings.TrimSuffix(strings.TrimPrefix(part, `URI="`), `"`)
				playlist := strings.Contains(line, "EXT-X-MEDIA") || strings.Contains(line, "EXT-X-I-FRAME-STREAM-INF") || strings.HasSuffix(strings.Split(value, "?")[0], ".m3u8")
				local, err := d.hlsLink(value, base, scope, parent, playlist, &added, ownerTime...)
				if err != nil {
					rewriteErr = err
					return part
				}
				return `URI="` + local + `"`
			})
			if rewriteErr != nil {
				return nil, rewriteErr
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		playlist := strings.HasSuffix(strings.ToLower(strings.Split(line, "?")[0]), ".m3u8") || i > 0 && strings.HasPrefix(strings.TrimSpace(lines[i-1]), "#EXT-X-STREAM-INF")
		local, err := d.hlsLink(strings.TrimSpace(line), base, scope, parent, playlist, &added, ownerTime...)
		if err != nil {
			return nil, err
		}
		lines[i] = local
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func (d *davServer) hlsLink(value string, base *url.URL, scope, parent string, playlist bool, added *[]string, ownerTime ...time.Time) (string, error) {
	u, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	u = base.ResolveReference(u)
	if err := validateMediaURL(u.String()); err != nil {
		return "", err
	}
	if len(ownerTime) == 0 || ownerTime[0].IsZero() {
		ownerTime = []time.Time{d.startedAt}
	}
	token, fresh, err := d.tokens.addLocked(u.String(), parent, playlist, ownerTime...)
	if err != nil {
		return "", err
	}
	if fresh {
		*added = append(*added, token)
	}
	return scope + token, nil
}

func (d *davServer) proxyMedia(w http.ResponseWriter, r *http.Request, raw string) {
	headers := http.Header{}
	if value := r.Header.Get("Range"); value != "" {
		headers.Set("Range", value)
	}
	if value := r.Header.Get("If-Range"); value != "" {
		headers.Set("If-Range", value)
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	resp, err := d.request(ctx, r.Method, raw, headers)
	if err != nil {
		davError(w, r, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusGone {
			davError(w, r, errMediaExpired)
			return
		}
		davError(w, r, fmt.Errorf("media HTTP %d", resp.StatusCode))
		return
	}
	if resp.StatusCode == http.StatusPartialContent && (resp.Header.Get("Content-Range") == "" || resp.ContentLength < 0) {
		davError(w, r, errors.New("upstream range has no length"))
		return
	}
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
		if value := resp.Header.Get(h); value != "" {
			w.Header().Set(h, value)
		}
	}
	if w.Header().Get("Content-Length") == "" && resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	w.WriteHeader(resp.StatusCode)
	_, copySpan := tracepkg.Begin(r.Context(), "dav.proxy.copy")
	fields := tracepkg.Fields{Outcome: "ok", Status: resp.StatusCode}
	if r.Method != http.MethodHead {
		idleTimeout := d.mediaIdleTimeout
		if idleTimeout <= 0 {
			idleTimeout = defaultMediaIdleTimeout
		}
		n, copyErr := io.Copy(w, &idleProgressReader{body: resp.Body, timeout: idleTimeout, cancel: cancel})
		fields.Bytes = n
		if copyErr != nil {
			fields.Outcome, fields.ErrorClass = "error", "copy"
			if errors.Is(copyErr, context.DeadlineExceeded) {
				fields.ErrorClass = "idle_timeout"
			} else if errors.Is(copyErr, context.Canceled) {
				fields.ErrorClass = "canceled"
			}
		}
		copySpan.End(fields)
		if copyErr != nil {
			// Headers and perhaps bytes are already on the wire; returning normally
			// would turn an unknown-length partial body into a valid HTTP 200.
			panic(http.ErrAbortHandler)
		}
		return
	}
	copySpan.End(fields)
}

// idleProgressReader bounds time without upstream bytes, not the lifetime of a
// video. A slow downstream writer does not count as an upstream stall. Canceling
// the request unblocks net/http's body.
type idleProgressReader struct {
	body     io.Reader
	timeout  time.Duration
	cancel   context.CancelFunc
	deadline time.Time
}

func (r *idleProgressReader) Read(p []byte) (int, error) {
	if r.deadline.IsZero() {
		r.deadline = time.Now().Add(r.timeout)
	}
	remaining := time.Until(r.deadline)
	if remaining <= 0 {
		r.cancel()
		return 0, context.DeadlineExceeded
	}
	var state atomic.Uint32 // 0 reading, 1 completed, 2 timed out
	timer := time.AfterFunc(remaining, func() {
		if state.CompareAndSwap(0, 2) {
			r.cancel()
		}
	})
	n, err := r.body.Read(p)
	state.CompareAndSwap(0, 1)
	timer.Stop()
	if state.Load() == 2 {
		return n, context.DeadlineExceeded
	}
	if n > 0 {
		r.deadline = time.Time{}
	}
	return n, err
}
