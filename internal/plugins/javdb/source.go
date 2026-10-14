// Package javdb adapts the public browser client's anonymous read protocol.
package javdb

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/core"
	tracepkg "github.com/fdkevin0/MetaDonalds/internal/trace"
)

const DefaultURL = "https://jdforrepam.com/api"

var itemID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

type Source struct {
	ID      string
	BaseURL string
	Client  *http.Client
	Logger  *slog.Logger
	// Top250BaseURL is the reference site's public snapshot directory.
	// Empty uses the current public default; tests may point it at a local fake.
	Top250BaseURL string
	cache         apiCache
}
type movie struct {
	ID, Title, Number, Summary, Score string
	ReleaseDate                       string `json:"release_date"`
}

type Media struct {
	ID              string       `json:"id"`
	Title           string       `json:"title"`
	Number          string       `json:"number"`
	Summary         string       `json:"summary"`
	Score           string       `json:"score"`
	ReleaseDate     string       `json:"release_date"`
	CoverURL        string       `json:"cover_url"`
	PreviewVideoURL string       `json:"preview_video_url"`
	Duration        int          `json:"duration"`
	Type            int          `json:"type"`
	Actors          []MediaActor `json:"actors"`
	PreviewImages   []struct {
		LargeURL string `json:"large_url"`
	} `json:"preview_images"`
}

type MediaActor struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

// Actor preserves the public profile's nullable and mixed-type fields.
func (s *Source) Actor(ctx context.Context, id string) (json.RawMessage, error) {
	if !itemID.MatchString(id) {
		return nil, fmt.Errorf("invalid actor ID")
	}
	var data struct {
		Actor json.RawMessage `json:"actor"`
	}
	if err := s.get(ctx, "/v1/actors/"+id, &data, func() bool {
		var actor MediaActor
		return json.Unmarshal(data.Actor, &actor) == nil && actor.ID == id
	}); err != nil {
		return nil, err
	}
	return data.Actor, nil
}

func (s *Source) item(m movie) core.Item {
	return core.Item{SourceID: s.ID, ID: m.ID, Title: m.Title, Code: m.Number, Summary: m.Summary, Score: m.Score, ReleaseDate: m.ReleaseDate}
}
func (s *Source) get(ctx context.Context, path string, out any, valid ...func() bool) error {
	endpoint := strings.TrimRight(s.BaseURL, "/") + path
	fresh := freshBrowse(ctx, endpoint)
	fetch := func(ctx context.Context, key string, load func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
		return s.cache.fetchMode(ctx, key, load, fresh, s.Logger)
	}
	data, err := fetch(ctx, endpoint, func(fetchCtx context.Context) (result json.RawMessage, err error) {
		host, route := traceEndpoint(endpoint)
		fetchCtx, span := tracepkg.Begin(fetchCtx, "javdb.http")
		fields := tracepkg.Fields{Outcome: "ok", Method: http.MethodGet, Host: host, Endpoint: route}
		defer func() {
			if err != nil {
				fields.Outcome = "error"
				if fields.ErrorClass == "" {
					fields.ErrorClass = "network"
				}
			}
			span.End(fields)
		}()
		req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, endpoint, nil)
		if err != nil {
			fields.ErrorClass = "request"
			return nil, err
		}
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("jdsignature", stamp+"."+signatureID+"."+fmt.Sprintf("%x", md5.Sum([]byte(stamp+signatureSalt))))
		resp, err := s.Client.Do(req)
		if err != nil {
			fields.ErrorClass = "network"
			return nil, fmt.Errorf("JavDB request failed: %w", err)
		}
		defer resp.Body.Close()
		fields.Status = resp.StatusCode
		if resp.StatusCode != 200 {
			fields.ErrorClass = "http_status"
			return nil, fmt.Errorf("JavDB returned HTTP %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, apiCacheBodyBytes+1))
		fields.Bytes = int64(len(body))
		if err != nil || len(body) > apiCacheBodyBytes {
			fields.ErrorClass = "read_or_limit"
			return nil, fmt.Errorf("invalid JavDB response")
		}
		var envelope struct {
			Success int             `json:"success"`
			Data    json.RawMessage `json:"data"`
		}
		_, validateSpan := tracepkg.Begin(fetchCtx, "javdb.validate")
		if json.Unmarshal(body, &envelope) != nil || envelope.Success != 1 || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
			validateSpan.End(tracepkg.Fields{Outcome: "error", ErrorClass: "invalid_response", Bytes: int64(len(body))})
			fields.ErrorClass = "invalid_response"
			return nil, fmt.Errorf("%w: invalid JavDB response", errInvalidResponse)
		}
		if json.Unmarshal(envelope.Data, out) != nil || len(valid) > 0 && !valid[0]() {
			validateSpan.End(tracepkg.Fields{Outcome: "error", ErrorClass: "invalid_response", Bytes: int64(len(body))})
			fields.ErrorClass = "invalid_response"
			return nil, fmt.Errorf("%w: invalid JavDB data", errInvalidResponse)
		}
		validateSpan.End(tracepkg.Fields{Outcome: "ok", Bytes: int64(len(body)), Count: 1})
		return envelope.Data, nil
	})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if json.Unmarshal(data, out) != nil {
		return fmt.Errorf("invalid JavDB data")
	}
	if len(valid) > 0 && !valid[0]() {
		return fmt.Errorf("invalid JavDB data")
	}
	return ctx.Err()
}
func (s *Source) Search(ctx context.Context, q, cursor string) (core.Page, error) {
	page := 1
	if cursor != "" {
		n, e := strconv.Atoi(cursor)
		if e != nil || n < 1 || n > 10000 {
			return core.Page{}, fmt.Errorf("invalid cursor")
		}
		page = n
	}
	const limit = 24
	values := url.Values{"q": {q}, "type": {"movie"}, "page": {strconv.Itoa(page)}, "limit": {strconv.Itoa(limit)}}
	var data struct {
		Movies []movie `json:"movies"`
	}
	if err := s.get(ctx, "/v2/search?"+values.Encode(), &data, func() bool { return data.Movies != nil }); err != nil {
		return core.Page{}, err
	}
	if data.Movies == nil {
		return core.Page{}, fmt.Errorf("missing movies")
	}
	out := core.Page{Items: []core.Item{}}
	for _, m := range data.Movies {
		if itemID.MatchString(m.ID) {
			out.Items = append(out.Items, s.item(m))
		}
	}
	// The upstream exposes no total/has_more; a full page is the only next-page signal.
	if len(data.Movies) == limit && page < 10000 {
		out.NextCursor = strconv.Itoa(page + 1)
	}
	return out, nil
}
func (s *Source) Detail(ctx context.Context, id string) (core.Item, error) {
	m, err := s.Media(ctx, id)
	if err != nil {
		return core.Item{}, err
	}
	return s.item(movie{ID: m.ID, Title: m.Title, Number: m.Number, Summary: m.Summary, Score: m.Score, ReleaseDate: m.ReleaseDate}), nil
}

func (s *Source) Latest(ctx context.Context, page int) (core.Page, error) {
	if page < 1 || page > 10 {
		return core.Page{}, fmt.Errorf("invalid page")
	}
	var data struct {
		Movies []movie `json:"movies"`
	}
	if err := s.get(ctx, "/v1/movies/latest?page="+strconv.Itoa(page)+"&filter_by=all", &data, func() bool { return data.Movies != nil }); err != nil {
		return core.Page{}, err
	}
	if data.Movies == nil {
		return core.Page{}, fmt.Errorf("missing movies")
	}
	out := core.Page{Items: []core.Item{}}
	for _, m := range data.Movies {
		if itemID.MatchString(m.ID) {
			out.Items = append(out.Items, s.item(m))
		}
	}
	if len(data.Movies) == 24 && page < 10 {
		out.NextCursor = strconv.Itoa(page + 1)
	}
	return out, nil
}

func (s *Source) Media(ctx context.Context, id string) (Media, error) {
	if !itemID.MatchString(id) {
		return Media{}, fmt.Errorf("invalid item ID")
	}
	var data struct {
		Movie Media `json:"movie"`
	}
	if err := s.get(ctx, "/v4/movies/"+id, &data, func() bool { return data.Movie.ID == id }); err != nil {
		return Media{}, err
	}
	if data.Movie.ID != id {
		return Media{}, fmt.Errorf("invalid movie identity")
	}
	return data.Movie, nil
}
func (s *Source) Resources(ctx context.Context, id string) ([]core.Resource, error) {
	if !itemID.MatchString(id) {
		return nil, fmt.Errorf("invalid item ID")
	}
	var data struct {
		Magnets []struct {
			Name, Hash string
			Size       int64
		} `json:"magnets"`
	}
	if err := s.get(ctx, "/v1/movies/"+id+"/magnets", &data, func() bool { return data.Magnets != nil }); err != nil {
		return nil, err
	}
	if data.Magnets == nil {
		return nil, fmt.Errorf("missing magnets")
	}
	out := []core.Resource{}
	seen := map[string]bool{}
	for _, m := range data.Magnets {
		h := strings.ToLower(m.Hash)
		if !core.HashPattern.MatchString(h) || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, core.Resource{ID: h, Kind: "magnet", Title: m.Name, InfoHash: h, Size: fmt.Sprintf("%d (upstream units)", m.Size)})
	}
	return out, nil
}
