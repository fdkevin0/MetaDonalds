package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/core"
	"github.com/fdkevin0/MetaDonalds/internal/plugins/javdb"
)

// flatMovies assembles one DAV directory from the upstream's bounded pages.
// The snapshot fixes its names, order and IDs for the same five-minute window.
func (d *davServer) flatMovies(ctx context.Context, dir string, maxPage int, load func(context.Context, int) (core.Page, error)) (*flatSnapshot, error) {
	return d.flat.load(ctx, "movies:"+path.Clean(dir), func(workCtx context.Context) (*flatSnapshot, error) {
		workCtx = javdb.FreshBrowseContext(workCtx)
		items := make([]core.Item, 0)
		seenIDs := map[string]bool{}
		payloadBytes := 0
		for page := 1; page <= maxPage; page++ {
			if err := workCtx.Err(); err != nil {
				return nil, err
			}
			batch, err := load(workCtx, page)
			if err != nil {
				return nil, err
			}
			if len(batch.Items) == 0 {
				break
			}
			newIDs := 0
			for _, item := range batch.Items {
				if !davID.MatchString(item.ID) || seenIDs[item.ID] {
					continue
				}
				payloadBytes += 512 + len(item.ID) + len(item.Code) + len(item.ReleaseDate) + len(item.Title) + len(item.Summary) + len(item.Score) + len(item.SourceID) + len(item.Size)
				for _, tag := range item.Tags {
					payloadBytes += len(tag) + 16
				}
				if payloadBytes > flatMaxBytes {
					return nil, errFlatListLimit
				}
				seenIDs[item.ID] = true
				items = append(items, core.Item{ID: item.ID, Code: item.Code, ReleaseDate: item.ReleaseDate})
				newIDs++
				if len(items) > flatMaxItems {
					return nil, errFlatListLimit
				}
			}
			if newIDs == 0 {
				return nil, errors.New("upstream repeated movie page")
			}
			if batch.NextCursor == "" || page == maxPage {
				break
			}
			if batch.NextCursor != strconv.Itoa(page+1) {
				return nil, errors.New("invalid upstream movie cursor")
			}
		}
		return flatMovieSnapshot(items), nil
	})
}

func flatMovieSnapshot(items []core.Item) *flatSnapshot {
	codeCount := map[string]int{}
	ids := map[string]bool{}
	for _, item := range items {
		ids[item.ID] = true
		if davID.MatchString(item.Code) {
			codeCount[item.Code]++
		}
	}
	snapshot := &flatSnapshot{nodes: make([]davNode, 0, len(items)), items: make(map[string]core.Item, len(items))}
	for i, item := range items {
		stem := item.ID
		if davID.MatchString(item.Code) && codeCount[item.Code] == 1 && (!ids[item.Code] || item.Code == item.ID) {
			stem = item.Code
		}
		name := fmt.Sprintf("%04d-%s", i+1, stem)
		snapshot.nodes = append(snapshot.nodes, davNode{name: name, dir: true, modTime: movieModTime(item.ReleaseDate, time.Time{})})
		snapshot.items[name] = item
		snapshot.bytes += len(name) + len(item.ID) + len(item.Code) + len(item.ReleaseDate) + 512
	}
	return snapshot
}

func (d *davServer) flatEntities(ctx context.Context, dir string, catalog davCatalog, kind, typ string) (*flatSnapshot, error) {
	return d.flat.load(ctx, "entities:"+path.Clean(dir), func(workCtx context.Context) (*flatSnapshot, error) {
		workCtx = javdb.FreshBrowseContext(workCtx)
		maxPage := 99
		if typ == "recommend" {
			maxPage = 1
		}
		entities := make([]javdb.BrowseEntity, 0)
		seenIDs := map[string]bool{}
		for page := 1; page <= maxPage; page++ {
			if err := workCtx.Err(); err != nil {
				return nil, err
			}
			batch, err := catalog.Directory(workCtx, kind, typ, page)
			if err != nil {
				return nil, err
			}
			if len(batch) == 0 {
				break
			}
			newIDs := 0
			for _, entity := range batch {
				if !davID.MatchString(entity.ID) || seenIDs[entity.ID] {
					continue
				}
				seenIDs[entity.ID] = true
				entities = append(entities, entity)
				newIDs++
				if len(entities) > flatMaxItems {
					return nil, errFlatListLimit
				}
			}
			if newIDs == 0 {
				return nil, errors.New("upstream repeated entity page")
			}
		}
		snapshot := &flatSnapshot{nodes: make([]davNode, 0, len(entities)), entities: map[string]javdb.BrowseEntity{}}
		for i, entity := range entities {
			name := fmt.Sprintf("%04d-%s", i+1, davReadableName(entity.Name, entity.ID))
			snapshot.nodes = append(snapshot.nodes, davNode{name: name, dir: true})
			snapshot.entities[name] = entity
			snapshot.bytes += len(name) + len(entity.ID) + len(entity.Name) + 128
		}
		return snapshot, nil
	})
}

func (d *davServer) flatTags(ctx context.Context, dir string, catalog davCatalog, typ string) (*flatSnapshot, error) {
	return d.flat.load(ctx, "tags:"+path.Clean(dir), func(workCtx context.Context) (*flatSnapshot, error) {
		groups, err := catalog.CategoryTags(javdb.FreshBrowseContext(workCtx), typ)
		if err != nil {
			return nil, err
		}
		snapshot := &flatSnapshot{nodes: make([]davNode, 0, len(groups)), tags: groups}
		for _, group := range groups {
			name := davReadableName(group.Name, group.ID)
			snapshot.nodes = append(snapshot.nodes, davNode{name: name, dir: true})
			snapshot.bytes += len(name) + 128
			for _, tag := range group.Tags {
				snapshot.bytes += len(tag.Name) + len(tag.ID) + 64
			}
		}
		return snapshot, nil
	})
}

func (d *davServer) flatItemNode(ctx context.Context, catalog davCatalog, parts []string, fullPath string, load func() (*flatSnapshot, error)) (davNode, error) {
	if len(parts) == 0 || len(parts[0]) > 110 {
		return davNode{}, os.ErrNotExist
	}
	// Media tokens are scoped by their local directory and outlive a refreshed
	// listing. Avoid fetching pages for every HLS segment.
	if len(parts) >= 2 && parts[1] == "media" {
		return d.tokenNode(ctx, parts, 2, fullPath)
	}
	if len(parts) >= 3 && parts[1] == "stream" && parts[2] == "media" {
		return d.tokenNode(ctx, parts, 3, fullPath)
	}
	snapshot, err := load()
	if err != nil {
		return davNode{}, err
	}
	item, ok := snapshot.items[parts[0]]
	if !ok {
		return davNode{}, os.ErrNotExist
	}
	resolved := append([]string{item.ID}, parts[1:]...)
	itemTime := movieModTime(item.ReleaseDate, snapshot.fetchedAt)
	node, err := d.itemNode(ctx, catalog, resolved, fullPath, itemTime)
	if err == nil {
		node.modTime = itemTime
		if len(parts) == 1 {
			node.name = parts[0]
		}
	}
	return node, err
}

func (d *davServer) flatLatestOrSearch(ctx context.Context, catalog davCatalog, name string, section, query string) (*flatSnapshot, error) {
	maxPage := 10
	if section == "search" {
		maxPage = 99
	}
	return d.flatMovies(ctx, name, maxPage, func(workCtx context.Context, page int) (core.Page, error) {
		if section == "latest" {
			return catalog.Latest(workCtx, page)
		}
		return catalog.Search(workCtx, query, strconv.Itoa(page))
	})
}

func (d *davServer) flatBrowseMovies(ctx context.Context, catalog davCatalog, name string, r davBrowsePath) (*flatSnapshot, error) {
	maxPage := 99
	if r.section == "rankings" {
		maxPage = 1
		if r.kind == "top250" {
			maxPage = 5
		}
	}
	return d.flatMovies(ctx, name, maxPage, func(workCtx context.Context, page int) (core.Page, error) {
		switch r.section {
		case "categories":
			return catalog.CategoryMovies(workCtx, r.typ, r.groupID, r.tagID, page)
		case "rankings":
			return catalog.RankingMovies(workCtx, r.kind, r.typ, r.period, page)
		case "actors", "series", "makers":
			return catalog.EntityMovies(workCtx, r.section, r.entityID, page)
		}
		return core.Page{}, os.ErrNotExist
	})
}

func flatParent(name string, parts []string, itemAt int) string {
	return path.Join("/", strings.Join(parts[:itemAt], "/"))
}
