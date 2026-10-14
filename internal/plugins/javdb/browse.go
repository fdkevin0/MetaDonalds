package javdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/fdkevin0/MetaDonalds/internal/core"
	tracepkg "github.com/fdkevin0/MetaDonalds/internal/trace"
)

const defaultTop250URL = "https://catembylegacy.fastcdn.dpdns.org/top250"

type BrowseEntity struct {
	ID, Name string
}

type BrowseTag struct {
	ID, Name string
}

type BrowseTagGroup struct {
	ID, Name string
	Tags     []BrowseTag
}

type browseEntityJSON struct {
	ID   json.RawMessage `json:"id"`
	Name string          `json:"name"`
	Type json.RawMessage `json:"type"`
}

func browseScalar(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

func browseType(name string, allowAll bool) (string, bool) {
	switch name {
	case "censored":
		return "0", true
	case "uncensored":
		return "1", true
	case "western":
		return "2", true
	case "fc2":
		return "3", true
	case "all":
		return "all", allowAll
	}
	return "", false
}

func browsePage(s *Source, movies []movie) core.Page {
	out := core.Page{Items: []core.Item{}}
	for _, m := range movies {
		if itemID.MatchString(m.ID) {
			out.Items = append(out.Items, s.item(m))
		}
	}
	return out
}

func browseNumber(page, max int) error {
	if page < 1 || page > max {
		return errors.New("invalid browse page")
	}
	return nil
}

// CategoryMovies follows the public category page's filter_by slot format.
func (s *Source) CategoryMovies(ctx context.Context, typ, groupID, tagID string, page int) (core.Page, error) {
	typeValue, ok := browseType(typ, false)
	if !ok || browseNumber(page, 99) != nil {
		return core.Page{}, errors.New("invalid category browse path")
	}
	slots := []string{typeValue, "t", "", "", "", "", ""}
	if tagID != "" {
		if !itemID.MatchString(tagID) || !itemID.MatchString(groupID) {
			return core.Page{}, errors.New("invalid category tag")
		}
		switch groupID {
		case "main":
			slots[2] = tagID
		case "year":
			slots[4] = tagID
		case "duration":
			slots[5] = tagID
		case "month":
			slots[6] = tagID
		default:
			slots[3] = tagID
		}
	} else if groupID != "" {
		return core.Page{}, errors.New("category tag is missing")
	}
	q := url.Values{"filter_by": {strings.Join(slots, ":")}, "sort_by": {"release"}, "order_by": {"desc"}, "page": {strconv.Itoa(page)}, "limit": {"24"}}
	var data struct {
		Movies []movie `json:"movies"`
	}
	if err := s.get(ctx, "/v1/movies/tags?"+q.Encode(), &data, func() bool { return data.Movies != nil }); err != nil {
		return core.Page{}, err
	}
	if data.Movies == nil {
		return core.Page{}, errors.New("category response lacks movies")
	}
	out := browsePage(s, data.Movies)
	if len(data.Movies) == 24 && page < 99 {
		out.NextCursor = strconv.Itoa(page + 1)
	}
	return out, nil
}

func (s *Source) CategoryTags(ctx context.Context, typ string) ([]BrowseTagGroup, error) {
	typeValue, ok := browseType(typ, false)
	if !ok {
		return nil, errors.New("invalid category type")
	}
	var data struct {
		Tags []struct {
			ID   json.RawMessage `json:"category_id"`
			Name string          `json:"category"`
			Tags []struct {
				ID   json.RawMessage `json:"id"`
				Name string          `json:"name"`
			} `json:"tags"`
		} `json:"tags"`
	}
	if err := s.get(ctx, "/v2/tags?type="+typeValue, &data, func() bool {
		if data.Tags == nil {
			return false
		}
		for _, group := range data.Tags {
			if !itemID.MatchString(browseScalar(group.ID)) || group.Tags == nil {
				return false
			}
		}
		return true
	}); err != nil {
		return nil, err
	}
	if data.Tags == nil {
		return nil, errors.New("category response lacks tag groups")
	}
	out := make([]BrowseTagGroup, 0, len(data.Tags))
	for _, group := range data.Tags {
		id := browseScalar(group.ID)
		if !itemID.MatchString(id) || group.Tags == nil {
			return nil, errors.New("invalid category tag group")
		}
		g := BrowseTagGroup{ID: id, Name: group.Name, Tags: []BrowseTag{}}
		for _, tag := range group.Tags {
			tagID := browseScalar(tag.ID)
			if itemID.MatchString(tagID) && tag.Name != "" {
				g.Tags = append(g.Tags, BrowseTag{ID: tagID, Name: tag.Name})
			}
		}
		out = append(out, g)
	}
	return out, nil
}

// RankingMovies exposes the public playback and score lists, plus the public
// TOP250 snapshot. Playback and score have only one upstream page.
func (s *Source) RankingMovies(ctx context.Context, kind, typ, period string, page int) (core.Page, error) {
	typeValue := typ
	if kind == "playback" {
		if typ != "all" && typ != "high_score" {
			return core.Page{}, errors.New("invalid playback filter")
		}
	} else {
		var ok bool
		typeValue, ok = browseType(typ, kind == "top250")
		if !ok {
			return core.Page{}, errors.New("invalid ranking type")
		}
	}
	if kind == "top250" {
		if period != "" || browseNumber(page, 5) != nil {
			return core.Page{}, errors.New("invalid TOP250 page")
		}
		return s.top250(ctx, typeValue, page)
	}
	if kind != "playback" && kind != "score" || page != 1 || period != "daily" && period != "weekly" && period != "monthly" {
		return core.Page{}, errors.New("invalid ranking path")
	}
	q := url.Values{"period": {period}}
	endpoint := "/v1/rankings/playback"
	if kind == "score" {
		endpoint = "/v1/rankings"
		q.Set("type", typeValue)
	} else {
		q.Set("filter_by", typeValue)
	}
	var data struct {
		Movies []movie `json:"movies"`
	}
	if err := s.get(ctx, endpoint+"?"+q.Encode(), &data, func() bool { return data.Movies != nil }); err != nil {
		return core.Page{}, err
	}
	if data.Movies == nil {
		return core.Page{}, errors.New("ranking response lacks movies")
	}
	return browsePage(s, data.Movies), nil
}

func (s *Source) top250(ctx context.Context, typeValue string, page int) (core.Page, error) {
	base := s.Top250BaseURL
	if base == "" {
		base = defaultTop250URL
	}
	endpoint := strings.TrimRight(base, "/") + "/" + typeValue + ".json"
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
		resp, err := s.Client.Do(req)
		if err != nil {
			fields.ErrorClass = "network"
			return nil, fmt.Errorf("TOP250 snapshot request failed: %w", err)
		}
		defer resp.Body.Close()
		fields.Status = resp.StatusCode
		if resp.StatusCode != http.StatusOK {
			fields.ErrorClass = "http_status"
			return nil, fmt.Errorf("TOP250 snapshot HTTP %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, apiCacheBodyBytes+1))
		fields.Bytes = int64(len(data))
		if err != nil || len(data) > apiCacheBodyBytes {
			fields.ErrorClass = "read_or_limit"
			return nil, errors.New("TOP250 snapshot exceeds 4 MiB")
		}
		var snapshot struct {
			Movies []movie `json:"movies"`
		}
		_, validateSpan := tracepkg.Begin(fetchCtx, "javdb.validate")
		if json.Unmarshal(data, &snapshot) != nil || snapshot.Movies == nil {
			validateSpan.End(tracepkg.Fields{Outcome: "error", ErrorClass: "invalid_response", Bytes: int64(len(data))})
			fields.ErrorClass = "invalid_response"
			return nil, fmt.Errorf("%w: invalid TOP250 snapshot", errInvalidResponse)
		}
		validateSpan.End(tracepkg.Fields{Outcome: "ok", Bytes: int64(len(data)), Count: len(snapshot.Movies)})
		return data, nil
	})
	if err != nil {
		return core.Page{}, err
	}
	if err := ctx.Err(); err != nil {
		return core.Page{}, err
	}
	var snapshot struct {
		Movies []movie `json:"movies"`
	}
	if json.Unmarshal(data, &snapshot) != nil || snapshot.Movies == nil {
		return core.Page{}, errors.New("invalid TOP250 snapshot")
	}
	start := (page - 1) * 50
	if start >= len(snapshot.Movies) {
		return core.Page{Items: []core.Item{}}, nil
	}
	end := min(start+50, len(snapshot.Movies))
	out := browsePage(s, snapshot.Movies[start:end])
	if end < len(snapshot.Movies) && page < 5 {
		out.NextCursor = strconv.Itoa(page + 1)
	}
	return out, nil
}

func (s *Source) Directory(ctx context.Context, kind, typ string, page int) ([]BrowseEntity, error) {
	if browseNumber(page, 99) != nil || kind != "actors" && kind != "series" && kind != "makers" {
		return nil, errors.New("invalid directory path")
	}
	if kind == "actors" && typ == "recommend" {
		if page != 1 {
			return nil, errors.New("actor recommendations are not paginated")
		}
		var data struct {
			Recommended []browseEntityJSON `json:"recommend_actors"`
			Monthly     []browseEntityJSON `json:"monthly_actors"`
			New         []browseEntityJSON `json:"new_actors"`
		}
		if err := s.get(ctx, "/v1/actors/recommend", &data, func() bool { return data.Recommended != nil || data.Monthly != nil || data.New != nil }); err != nil {
			return nil, err
		}
		if data.Recommended == nil && data.Monthly == nil && data.New == nil {
			return nil, errors.New("actor recommendation response lacks lists")
		}
		return directoryEntities(append(append(data.Recommended, data.Monthly...), data.New...)), nil
	}
	typeValue, ok := browseType(typ, false)
	if !ok || kind == "actors" && typeValue != "0" && typeValue != "1" {
		return nil, errors.New("invalid directory type")
	}
	q := url.Values{"type": {typeValue}, "page": {strconv.Itoa(page)}}
	var data struct {
		Actors []browseEntityJSON `json:"actors"`
		Series []browseEntityJSON `json:"series"`
		Makers []browseEntityJSON `json:"makers"`
	}
	if err := s.get(ctx, "/v1/"+kind+"?"+q.Encode(), &data, func() bool {
		switch kind {
		case "actors":
			return data.Actors != nil
		case "series":
			return data.Series != nil
		default:
			return data.Makers != nil
		}
	}); err != nil {
		return nil, err
	}
	var entities []browseEntityJSON
	switch kind {
	case "actors":
		entities = data.Actors
	case "series":
		entities = data.Series
	case "makers":
		entities = data.Makers
	}
	if entities == nil {
		return nil, errors.New("directory response lacks entities")
	}
	return directoryEntities(entities), nil
}

func directoryEntities(raw []browseEntityJSON) []BrowseEntity {
	out := []BrowseEntity{}
	seen := map[string]bool{}
	for _, e := range raw {
		id := browseScalar(e.ID)
		if !itemID.MatchString(id) || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, BrowseEntity{ID: id, Name: e.Name})
	}
	return out
}

func (s *Source) EntityMovies(ctx context.Context, kind, id string, page int) (core.Page, error) {
	if (kind != "actors" && kind != "series" && kind != "makers") || !itemID.MatchString(id) || browseNumber(page, 99) != nil {
		return core.Page{}, errors.New("invalid entity movies path")
	}
	var data struct {
		Actor  browseEntityJSON `json:"actor"`
		Series browseEntityJSON `json:"series"`
		Maker  browseEntityJSON `json:"maker"`
	}
	if err := s.get(ctx, "/v1/"+kind+"/"+id, &data, func() bool {
		var e browseEntityJSON
		switch kind {
		case "actors":
			e = data.Actor
		case "series":
			e = data.Series
		default:
			e = data.Maker
		}
		typ := browseScalar(e.Type)
		return browseScalar(e.ID) == id && (typ == "0" || typ == "1" || typ == "2" || typ == "3")
	}); err != nil {
		return core.Page{}, err
	}
	entity := data.Actor
	letter := "a"
	if kind == "series" {
		entity, letter = data.Series, "s"
	} else if kind == "makers" {
		entity, letter = data.Maker, "m"
	}
	if browseScalar(entity.ID) != id {
		return core.Page{}, errors.New("entity detail identity mismatch")
	}
	typeValue := browseScalar(entity.Type)
	if typeValue != "0" && typeValue != "1" && typeValue != "2" && typeValue != "3" {
		return core.Page{}, errors.New("entity detail has invalid type")
	}
	q := url.Values{"filter_by": {typeValue + ":" + letter + ":" + id + ":"}, "sort_by": {"release"}, "order_by": {"desc"}, "page": {strconv.Itoa(page)}, "limit": {"24"}}
	var movies struct {
		Movies []movie `json:"movies"`
	}
	if err := s.get(ctx, "/v1/movies/tags?"+q.Encode(), &movies, func() bool { return movies.Movies != nil }); err != nil {
		return core.Page{}, err
	}
	if movies.Movies == nil {
		return core.Page{}, errors.New("entity movie response lacks movies")
	}
	out := browsePage(s, movies.Movies)
	if len(movies.Movies) == 24 && page < 99 {
		out.NextCursor = strconv.Itoa(page + 1)
	}
	return out, nil
}
