package backend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/core"
	"github.com/fdkevin0/MetaDonalds/internal/plugins/javdb"
	tracepkg "github.com/fdkevin0/MetaDonalds/internal/trace"
	"golang.org/x/net/webdav"
)

type davCatalog interface {
	Latest(context.Context, int) (core.Page, error)
	Search(context.Context, string, string) (core.Page, error)
	Media(context.Context, string) (javdb.Media, error)
	Actor(context.Context, string) (json.RawMessage, error)
	CategoryMovies(context.Context, string, string, string, int) (core.Page, error)
	CategoryTags(context.Context, string) ([]javdb.BrowseTagGroup, error)
	RankingMovies(context.Context, string, string, string, int) (core.Page, error)
	Directory(context.Context, string, string, int) ([]javdb.BrowseEntity, error)
	EntityMovies(context.Context, string, string, int) (core.Page, error)
}

type davServer struct {
	catalogs         map[string]davCatalog
	flat             flatCache
	startedAt        time.Time
	client           *http.Client
	resolveBase      string
	tokens           *mediaTokens
	playlists        *playlistCache
	webdav           *webdav.Handler
	mediaIdleTimeout time.Duration
}

type davNode struct {
	name, kind, mime, url, scope, parent string
	size                                 int64
	dir                                  bool
	data                                 []byte
	media                                javdb.Media
	actor                                javdb.MediaActor
	modTime                              time.Time
}

type davFS struct{ server *davServer }
type davMemoKey struct{}
type davMemo struct {
	snapshots map[string]*flatSnapshot
	nodes     map[string]davNode
	children  map[string][]davNode
	images    map[string]davImage
}

type davImage struct {
	data []byte
	mime string
}

var davID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
var errMediaDirectoryLimit = errors.New("media directory exceeds 2000 links")

func newDAV(catalogs map[string]davCatalog, client *http.Client, resolveBase string) http.Handler {
	if client == nil {
		client = safeMediaClient()
	}
	if resolveBase == "" {
		resolveBase = "https://catembylegacy.fastcdn.dpdns.org"
	}
	d := &davServer{catalogs: catalogs, client: client, resolveBase: resolveBase, tokens: newMediaTokens(), playlists: newPlaylistCache(), startedAt: time.Now().UTC(), mediaIdleTimeout: defaultMediaIdleTimeout}
	d.webdav = &webdav.Handler{Prefix: "/dav", FileSystem: davFS{d}, LockSystem: webdav.NewMemLS()}
	return d
}

func (d *davServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if trace := davTraceFrom(r.Context()); trace != nil {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) > 1 && d.catalogs[parts[1]] != nil {
			trace.mu.Lock()
			trace.source = parts[1]
			trace.mu.Unlock()
		}
	}
	r = r.WithContext(context.WithValue(r.Context(), davMemoKey{}, &davMemo{nodes: map[string]davNode{}, children: map[string][]davNode{}, images: map[string]davImage{}}))
	if r.Method == http.MethodOptions {
		w.Header().Set("Allow", "OPTIONS, GET, HEAD, PROPFIND")
		w.Header().Set("DAV", "1")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != "PROPFIND" {
		http.Error(w, "WebDAV is read-only", http.StatusForbidden)
		return
	}
	if r.Method == "PROPFIND" {
		depth := r.Header.Get("Depth")
		if depth != "0" && depth != "1" {
			http.Error(w, "Depth must be 0 or 1", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	}
	if !strings.HasPrefix(r.URL.Path, "/dav/") || strings.Contains(r.URL.Path, "\\") {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/dav")
	node, err := d.lookup(r.Context(), name)
	if err != nil {
		davError(w, r, err)
		return
	}
	if r.Method == "PROPFIND" {
		if r.Header.Get("Depth") == "1" && node.dir {
			if _, err := d.children(r.Context(), name, node); err != nil {
				davError(w, r, err)
				return
			}
		}
		d.webdav.ServeHTTP(w, r)
		return
	}
	if node.dir {
		http.Error(w, "directory: use a WebDAV client", http.StatusMethodNotAllowed)
		return
	}
	d.serveFile(w, r, node)
}

func davError(w http.ResponseWriter, r *http.Request, err error) {
	if trace := davTraceFrom(r.Context()); trace != nil {
		trace.mu.Lock()
		stage := trace.failureStage
		trace.mu.Unlock()
		if stage == "" {
			trace.recordFailure("lookup", err)
		}
	}
	if errors.Is(err, errExpiredToken) || errors.Is(err, errMediaExpired) {
		http.Error(w, "media link expired; reopen the playlist", http.StatusGone)
		return
	}
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, os.ErrPermission) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if errors.Is(err, errTokenCapacity) {
		http.Error(w, "too many active media links; retry later", http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, errMediaDirectoryLimit) {
		http.Error(w, "media directory exceeds 2000 links", http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		http.Error(w, "media directory or upstream request timed out", http.StatusGatewayTimeout)
		return
	}
	http.Error(w, "upstream media unavailable", http.StatusBadGateway)
}

func (d *davServer) lookup(ctx context.Context, name string) (davNode, error) {
	if memo, ok := ctx.Value(davMemoKey{}).(*davMemo); ok {
		if n, found := memo.nodes[name]; found {
			return n, nil
		}
	}
	n, err := d.lookupFresh(ctx, name)
	if err == nil {
		if memo, ok := ctx.Value(davMemoKey{}).(*davMemo); ok {
			memo.nodes[name] = n
		}
	}
	return n, err
}

func (d *davServer) lookupFresh(ctx context.Context, name string) (davNode, error) {
	if name == "/" {
		return davNode{name: "dav", dir: true, modTime: d.startedAt}, nil
	}
	parts := strings.Split(strings.Trim(name, "/"), "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return davNode{}, os.ErrNotExist
		}
	}
	if len(parts) == 0 {
		return davNode{}, os.ErrNotExist
	}
	source := parts[0]
	catalog := d.catalogs[source]
	if catalog == nil {
		return davNode{}, os.ErrNotExist
	}
	if len(parts) == 1 {
		return davNode{name: source, dir: true, modTime: d.startedAt}, nil
	}
	if parts[1] == "latest" || parts[1] == "search" {
		if parts[1] == "search" && len(parts) == 2 {
			return davNode{name: "search", dir: true, modTime: d.startedAt}, nil
		}
		if parts[1] == "search" && len(parts) == 3 && parts[2] == "README.txt" {
			b := []byte("Open /dav/" + source + "/search/<URL-encoded-query>/ to search.\n")
			return davNode{name: "README.txt", kind: "bytes", mime: "text/plain", data: b, size: int64(len(b)), modTime: d.startedAt}, nil
		}
		itemAt, query := 2, ""
		if parts[1] == "search" {
			if len(parts) < 3 || !validSearchPath(parts[2]) {
				return davNode{}, os.ErrNotExist
			}
			itemAt, query = 3, parts[2]
		}
		dir := path.Join("/", strings.Join(parts[:itemAt], "/"))
		load := func() (*flatSnapshot, error) {
			return d.flatLatestOrSearch(ctx, catalog, dir, parts[1], query)
		}
		if len(parts) == itemAt {
			snapshot, err := load()
			if err != nil {
				return davNode{}, err
			}
			return davNode{name: parts[itemAt-1], dir: true, modTime: snapshot.fetchedAt}, nil
		}
		return d.flatItemNode(ctx, catalog, parts[itemAt:], name, load)
	}
	if browseSection(parts[1]) {
		return d.browseLookup(ctx, catalog, parts, name)
	}
	return davNode{}, os.ErrNotExist
}

func validSearchPath(s string) bool {
	return len(s) > 0 && len(s) <= 200 && s != "." && s != ".." && !strings.ContainsAny(s, "\\/\x00\r\n")
}

func (d *davServer) itemNode(ctx context.Context, catalog davCatalog, parts []string, fullPath string, ownerTime ...time.Time) (davNode, error) {
	if len(parts) == 0 || !davID.MatchString(parts[0]) {
		return davNode{}, os.ErrNotExist
	}
	detailCtx, detailSpan := tracepkg.Begin(ctx, "dav.detail")
	m, err := catalog.Media(detailCtx, parts[0])
	if err != nil {
		detailSpan.End(tracepkg.Fields{Outcome: "error", ErrorClass: davFailureCategory(err)})
	} else {
		detailSpan.End(tracepkg.Fields{Outcome: "ok", Count: 1})
	}
	if err != nil {
		return davNode{}, err
	}
	itemTime := movieModTime(m.ReleaseDate, d.startedAt)
	if len(ownerTime) > 0 && !ownerTime[0].IsZero() {
		itemTime = ownerTime[0]
	}
	if len(parts) == 1 {
		return davNode{name: parts[0], dir: true, media: m, modTime: itemTime}, nil
	}
	switch parts[1] {
	case "actors":
		if len(parts) == 2 {
			return davNode{name: "actors", kind: "actors", dir: true, media: m}, nil
		}
		for _, actor := range davActorDirs(m, itemTime) {
			if parts[2] == actor.name {
				return d.actorNode(ctx, catalog, actor, parts[3:])
			}
		}
	case "metadata.json":
		if len(parts) != 2 {
			break
		}
		data, _ := json.MarshalIndent(struct {
			ID          string `json:"id"`
			Code        string `json:"code"`
			Title       string `json:"title"`
			Summary     string `json:"summary"`
			Score       string `json:"score"`
			ReleaseDate string `json:"release_date"`
			Duration    int    `json:"duration_minutes"`
		}{m.ID, m.Number, m.Title, m.Summary, m.Score, m.ReleaseDate, m.Duration}, "", "  ")
		data = append(data, '\n')
		return davNode{name: "metadata.json", kind: "bytes", mime: "application/json", data: data, size: int64(len(data))}, nil
	case "cover.jpg":
		if len(parts) == 2 && m.CoverURL != "" {
			return d.imageNode(ctx, "cover.jpg", m.CoverURL)
		}
	case "images":
		if len(parts) == 2 {
			return davNode{name: "images", dir: true, media: m}, nil
		}
		if len(parts) == 3 {
			index, e := strconv.Atoi(strings.TrimSuffix(parts[2], ".jpg"))
			if e == nil && strings.HasSuffix(parts[2], ".jpg") && index >= 1 && index <= len(m.PreviewImages) && index <= 30 {
				return d.imageNode(ctx, parts[2], m.PreviewImages[index-1].LargeURL)
			}
		}
	case "preview.m3u8":
		if len(parts) == 2 && m.PreviewVideoURL != "" {
			return d.playlistNode(ctx, "preview.m3u8", m.PreviewVideoURL, "media/", path.Join(path.Dir(fullPath), "media"), itemTime)
		}
	case "stream":
		if m.Number == "" {
			return davNode{}, os.ErrNotExist
		}
		if len(parts) == 2 {
			return davNode{name: "stream", dir: true, media: m}, nil
		}
		if parts[2] == "media" {
			return d.tokenNode(ctx, parts, 3, fullPath)
		}
		if len(parts) == 3 {
			vs, e := d.resolve(ctx, m)
			if e != nil {
				return davNode{}, e
			}
			for i, v := range vs {
				name := variantName(i, v)
				if parts[2] == name {
					return d.variantNode(ctx, name, v, path.Join(path.Dir(fullPath), "media"), itemTime)
				}
			}
		}
	case "media":
		return d.tokenNode(ctx, parts, 2, fullPath)
	}
	return davNode{}, os.ErrNotExist
}

func movieModTime(release string, fallback time.Time) time.Time {
	if date, err := time.Parse("2006-01-02", release); err == nil && date.Year() >= 1900 && date.Year() <= 2100 {
		return date.UTC()
	}
	return fallback
}

func (d *davServer) tokenNode(ctx context.Context, parts []string, pos int, fullPath string) (davNode, error) {
	if len(parts) == pos {
		return davNode{name: "media", dir: true, parent: path.Clean(fullPath), modTime: d.startedAt}, nil
	}
	if len(parts) != pos+1 {
		return davNode{}, os.ErrNotExist
	}
	entry, err := d.tokens.get(parts[pos], path.Dir(fullPath))
	if err != nil {
		return davNode{}, err
	}
	if entry.playlist {
		return d.playlistNode(ctx, parts[pos], entry.url, "", entry.parent, entry.modTime)
	}
	size, mime, err := d.remoteSize(ctx, entry.url)
	if err != nil {
		return davNode{}, err
	}
	if size < 0 {
		return davNode{}, errors.New("upstream segment size unavailable")
	}
	return davNode{name: parts[pos], kind: "proxy", url: entry.url, mime: mime, size: size, modTime: entry.modTime}, nil
}

func (d *davServer) children(ctx context.Context, name string, n davNode) ([]davNode, error) {
	if memo, ok := ctx.Value(davMemoKey{}).(*davMemo); ok {
		if c, found := memo.children[name]; found {
			return c, nil
		}
	}
	children, err := d.childrenFresh(ctx, name, n)
	if err == nil {
		// Browse children may come directly from an immutable cached snapshot.
		// Update only a request-local copy when a child has its own newer snapshot.
		children = append([]davNode(nil), children...)
		for i := range children {
			if children[i].dir {
				if current := d.flat.modTime(ctx, path.Join(name, children[i].name)); !current.IsZero() {
					children[i].modTime = current
				}
				if children[i].modTime.IsZero() {
					children[i].modTime = d.startedAt
				}
			}
		}
		if memo, ok := ctx.Value(davMemoKey{}).(*davMemo); ok {
			memo.children[name] = children
			for _, child := range children {
				memo.nodes[path.Join(name, child.name)] = child
			}
		}
	}
	return children, err
}

func (d *davServer) childrenFresh(ctx context.Context, name string, n davNode) ([]davNode, error) {
	parts := strings.Split(strings.Trim(name, "/"), "/")
	if name == "/" {
		out := make([]davNode, 0, len(d.catalogs))
		for id := range d.catalogs {
			out = append(out, davNode{name: id, dir: true})
		}
		return out, nil
	}
	if len(parts) == 1 {
		return davDirs([]string{"latest", "search", "categories", "rankings", "actors", "series", "makers"}), nil
	}
	if parts[1] == "latest" && len(parts) == 2 {
		snapshot, err := d.flatLatestOrSearch(ctx, d.catalogs[parts[0]], name, "latest", "")
		if err != nil {
			return nil, err
		}
		return snapshot.nodes, nil
	}
	if parts[1] == "search" && len(parts) == 2 {
		b := []byte("Open /dav/" + parts[0] + "/search/<URL-encoded-query>/ to search.\n")
		return []davNode{{name: "README.txt", kind: "bytes", mime: "text/plain", data: b, size: int64(len(b)), modTime: d.startedAt}}, nil
	}
	if parts[1] == "search" && len(parts) == 3 {
		snapshot, err := d.flatLatestOrSearch(ctx, d.catalogs[parts[0]], name, "search", parts[2])
		if err != nil {
			return nil, err
		}
		return snapshot.nodes, nil
	}
	if n.kind == "actors" {
		return davActorDirs(n.media, n.modTime), nil
	}
	if n.kind == "actor" {
		out := []davNode{}
		for _, file := range []string{"metadata.json", "avatar.jpg"} {
			if file == "avatar.jpg" && n.actor.AvatarURL == "" {
				continue
			}
			child, err := d.actorNode(ctx, d.catalogs[parts[0]], n, []string{file})
			if err != nil {
				return nil, err
			}
			out = append(out, child)
		}
		return out, nil
	}
	if n.name == "images" {
		out := []davNode{}
		for i, image := range n.media.PreviewImages {
			if i == 30 {
				break
			}
			child, err := d.imageNode(ctx, fmt.Sprintf("%02d.jpg", i+1), image.LargeURL)
			if err != nil {
				return nil, err
			}
			child.modTime = n.modTime
			out = append(out, child)
		}
		return out, nil
	}
	if n.name == "stream" {
		vs, err := d.resolve(ctx, n.media)
		if err != nil {
			return nil, err
		}
		out := make([]davNode, 0, len(vs)+1)
		for i, v := range vs {
			child, err := d.variantNode(ctx, variantName(i, v), v, path.Join(name, "media"), n.modTime)
			if err != nil {
				return nil, err
			}
			child.modTime = n.modTime
			out = append(out, child)
		}
		out = append(out, davNode{name: "media", dir: true, parent: path.Join(name, "media"), modTime: n.modTime})
		return out, nil
	}
	if n.name == "media" {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		out := []davNode{}
		seen := make(map[string]bool)
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			keys := d.tokens.list(n.parent)
			if len(keys) > 2000 {
				return nil, errMediaDirectoryLimit
			}
			added := false
			for _, key := range keys {
				if seen[key] {
					continue
				}
				child, err := d.lookup(ctx, path.Join(n.parent, key))
				if err != nil {
					return nil, err
				}
				seen[key] = true
				out = append(out, child)
				added = true
			}
			if !added {
				return out, nil
			}
		}
	}
	if n.media.ID != "" {
		out := []davNode{}
		for _, child := range []string{"metadata.json", "cover.jpg", "images", "preview.m3u8", "media", "stream", "actors"} {
			if child == "cover.jpg" && n.media.CoverURL == "" || child == "preview.m3u8" && n.media.PreviewVideoURL == "" || child == "images" && len(n.media.PreviewImages) == 0 || child == "stream" && n.media.Number == "" || child == "actors" && len(n.media.Actors) == 0 {
				continue
			}
			if child == "media" {
				if n.media.PreviewVideoURL != "" {
					out = append(out, davNode{name: "media", dir: true, parent: path.Join(name, "media"), modTime: n.modTime})
				}
				continue
			}
			if child == "stream" {
				out = append(out, davNode{name: "stream", dir: true, media: n.media, modTime: n.modTime})
				continue
			}
			if child == "images" {
				out = append(out, davNode{name: "images", dir: true, media: n.media, modTime: n.modTime})
				continue
			}
			if child == "actors" {
				out = append(out, davNode{name: "actors", kind: "actors", dir: true, media: n.media, modTime: n.modTime})
				continue
			}
			catalog := d.catalogs[parts[0]]
			c, err := d.itemNode(ctx, catalog, []string{n.media.ID, child}, path.Join(name, child), n.modTime)
			if err != nil {
				return nil, err
			}
			c.modTime = n.modTime
			out = append(out, c)
		}
		return out, nil
	}
	if browseSection(parts[1]) {
		return d.browseChildren(ctx, parts)
	}
	return nil, nil
}

func (fs davFS) Mkdir(context.Context, string, os.FileMode) error { return os.ErrPermission }
func (fs davFS) RemoveAll(context.Context, string) error          { return os.ErrPermission }
func (fs davFS) Rename(context.Context, string, string) error     { return os.ErrPermission }
func (fs davFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	n, err := fs.server.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	return davInfo{davNode: n, server: fs.server}, nil
}
func (fs davFS) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	if flag != os.O_RDONLY {
		return nil, os.ErrPermission
	}
	n, err := fs.server.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	return &davFile{server: fs.server, ctx: ctx, name: name, node: n, Reader: bytes.NewReader(n.data)}, nil
}

type davInfo struct {
	davNode
	server *davServer
}

func (i davInfo) Name() string { return i.name }
func (i davInfo) Size() int64  { return i.size }
func (i davInfo) Mode() os.FileMode {
	if i.dir {
		return os.ModeDir | 0555
	}
	return 0444
}
func (i davInfo) ModTime() time.Time {
	if !i.modTime.IsZero() {
		return i.modTime
	}
	return time.Unix(0, 0)
}
func (i davInfo) IsDir() bool { return i.dir }
func (i davInfo) Sys() any    { return nil }
func (i davInfo) ETag(ctx context.Context) (string, error) {
	if i.kind == "playlist" || i.kind == "bytes" {
		return fmt.Sprintf(`"%x"`, sha256.Sum256(i.data)), nil
	}
	if i.kind == "image" {
		data, _, err := i.server.imageDataMemo(ctx, i.url)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`"%x"`, sha256.Sum256(data)), nil
	}
	return "", webdav.ErrNotImplemented
}
func (i davInfo) ContentType(context.Context) (string, error) {
	if i.mime != "" {
		return i.mime, nil
	}
	return "application/octet-stream", nil
}

type davFile struct {
	*bytes.Reader
	server *davServer
	ctx    context.Context
	name   string
	node   davNode
	pos    int
}

func (f *davFile) Close() error { return nil }
func (f *davFile) Stat() (os.FileInfo, error) {
	return davInfo{davNode: f.node, server: f.server}, nil
}
func (f *davFile) Write([]byte) (int, error) { return 0, os.ErrPermission }
func (f *davFile) Readdir(count int) ([]os.FileInfo, error) {
	if !f.node.dir {
		return nil, os.ErrInvalid
	}
	children, err := f.server.children(f.ctx, f.name, f.node)
	if err != nil {
		return nil, err
	}
	if f.pos >= len(children) {
		if count <= 0 {
			return []os.FileInfo{}, nil
		}
		return nil, io.EOF
	}
	end := len(children)
	if count > 0 && f.pos+count < end {
		end = f.pos + count
	}
	out := make([]os.FileInfo, 0, end-f.pos)
	for _, n := range children[f.pos:end] {
		out = append(out, davInfo{davNode: n, server: f.server})
	}
	f.pos = end
	return out, nil
}

func (d *davServer) serveFile(w http.ResponseWriter, r *http.Request, n davNode) {
	switch n.kind {
	case "bytes":
		serveGenerated(w, r, n, n.data, n.mime)
	case "image":
		data, mime, err := d.imageDataMemo(r.Context(), n.url)
		if err != nil {
			davError(w, r, err)
			return
		}
		serveGenerated(w, r, n, data, mime)
	case "playlist":
		serveGenerated(w, r, n, n.data, "application/vnd.apple.mpegurl")
	case "proxy":
		d.proxyMedia(w, r, n.url)
	default:
		http.NotFound(w, r)
	}
}

// The displayed movie date is historical metadata, not a safe HTTP cache
// validator: bytes may change without the release date changing. Content ETags
// govern conditionals; date-only If-Modified-Since is deliberately ignored,
// and a date If-Range falls back to a full response.
func serveGenerated(w http.ResponseWriter, r *http.Request, n davNode, data []byte, mime string) {
	w.Header().Set("Content-Type", mime)
	w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sha256.Sum256(data)))
	w.Header().Set("Cache-Control", "no-cache")
	if !n.modTime.IsZero() && !n.modTime.After(time.Now()) {
		w.Header().Set("Last-Modified", n.modTime.UTC().Format(http.TimeFormat))
	}
	request := r.Clone(r.Context())
	request.Header = r.Header.Clone()
	request.Header.Del("If-Modified-Since")
	request.Header.Del("If-Unmodified-Since")
	http.ServeContent(w, request, n.name, time.Time{}, bytes.NewReader(data))
}
