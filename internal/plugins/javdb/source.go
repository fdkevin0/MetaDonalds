// Package javdb adapts the public browser client's anonymous read protocol.
package javdb

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/core"
)

const DefaultURL = "https://jdforrepam.com/api"

var itemID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

type Source struct {
	ID      string
	BaseURL string
	Client  *http.Client
}
type movie struct{ ID, Title, Number, Summary, Score string }

func (s *Source) item(m movie) core.Item {
	return core.Item{SourceID: s.ID, ID: m.ID, Title: m.Title, Code: m.Number, Summary: m.Summary, Score: m.Score}
}
func (s *Source) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.BaseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("jdsignature", stamp+"."+signatureID+"."+fmt.Sprintf("%x", md5.Sum([]byte(stamp+signatureSalt))))
	resp, err := s.Client.Do(req)
	if err != nil {
		return fmt.Errorf("JavDB request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("JavDB returned HTTP %d", resp.StatusCode)
	}
	var envelope struct {
		Success int             `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope); err != nil || envelope.Success != 1 || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("invalid JavDB response")
	}
	if json.Unmarshal(envelope.Data, out) != nil {
		return fmt.Errorf("invalid JavDB data")
	}
	return nil
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
	if err := s.get(ctx, "/v2/search?"+values.Encode(), &data); err != nil {
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
	if !itemID.MatchString(id) {
		return core.Item{}, fmt.Errorf("invalid item ID")
	}
	var data struct {
		Movie movie `json:"movie"`
	}
	if err := s.get(ctx, "/v4/movies/"+id, &data); err != nil {
		return core.Item{}, err
	}
	if data.Movie.ID != id {
		return core.Item{}, fmt.Errorf("invalid movie identity")
	}
	return s.item(data.Movie), nil
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
	if err := s.get(ctx, "/v1/movies/"+id+"/magnets", &data); err != nil {
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
