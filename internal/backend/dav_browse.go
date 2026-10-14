package backend

import (
	"context"
	"os"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

var davMovieTypes = []string{"censored", "uncensored", "western", "fc2"}
var davRankingTypes = []string{"all", "censored", "uncensored", "western", "fc2"}
var davPlaybackTypes = []string{"all", "high_score"}
var davPeriods = []string{"daily", "weekly", "monthly"}

func browseSection(s string) bool {
	return s == "categories" || s == "rankings" || s == "actors" || s == "series" || s == "makers"
}

func inBrowseList(value string, options []string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

// Names shown to DAV clients are real path components, not displayname only.
func davReadableName(name, id string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\?#%<>:"|*`, r) {
			return ' '
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		name = "Item"
	}
	if utf8.RuneCountInString(name) > 80 {
		name = string([]rune(name)[:80])
	}
	return name + " [" + id + "]"
}

func davLabelID(label string) (string, bool) {
	start := strings.LastIndex(label, " [")
	if start < 1 || !strings.HasSuffix(label, "]") {
		return "", false
	}
	id := strings.TrimSuffix(label[start+2:], "]")
	return id, davID.MatchString(id)
}

func davDirs(names []string) []davNode {
	out := make([]davNode, len(names))
	for i, name := range names {
		out[i] = davNode{name: name, dir: true}
	}
	return out
}

type davBrowsePath struct {
	section, node, typ, kind, period, groupID, tagID, entityID, entityName string
	itemAt                                                                 int
}

func parseDAVBrowse(parts []string) (davBrowsePath, error) {
	r := davBrowsePath{section: parts[1]}
	switch r.section {
	case "categories":
		if len(parts) == 2 {
			r.node = "categories-root"
			return r, nil
		}
		r.typ = parts[2]
		if !inBrowseList(r.typ, davMovieTypes) {
			return r, os.ErrNotExist
		}
		if len(parts) == 3 {
			r.node, r.itemAt = "categories-type", 3
			return r, nil
		}
		if parts[3] == "tags" {
			if len(parts) == 4 {
				r.node = "categories-tags"
				return r, nil
			}
			var ok bool
			r.groupID, ok = davLabelID(parts[4])
			if !ok {
				return r, os.ErrNotExist
			}
			if len(parts) == 5 {
				r.node = "categories-group"
				return r, nil
			}
			r.tagID, ok = davLabelID(parts[5])
			if !ok {
				return r, os.ErrNotExist
			}
			if len(parts) == 6 {
				r.node, r.itemAt = "categories-tag", 6
				return r, nil
			}
			r.node, r.itemAt = "movies", 6
			return r, nil
		}
		r.node, r.itemAt = "movies", 3
		return r, nil
	case "rankings":
		if len(parts) == 2 {
			r.node = "rankings-root"
			return r, nil
		}
		r.kind = parts[2]
		if r.kind != "playback" && r.kind != "score" && r.kind != "top250" {
			return r, os.ErrNotExist
		}
		if len(parts) == 3 {
			r.node = "rankings-kind"
			return r, nil
		}
		r.typ = parts[3]
		valid := inBrowseList(r.typ, davRankingTypes)
		if r.kind == "playback" {
			valid = inBrowseList(r.typ, davPlaybackTypes)
		} else if r.kind == "score" {
			valid = inBrowseList(r.typ, davMovieTypes)
		}
		if !valid {
			return r, os.ErrNotExist
		}
		if r.kind == "top250" {
			r.itemAt = 4
			if len(parts) == 4 {
				r.node = "rankings-type"
			} else {
				r.node = "movies"
			}
			return r, nil
		}
		if len(parts) == 4 {
			r.node = "rankings-type"
			return r, nil
		}
		r.period = parts[4]
		if !inBrowseList(r.period, davPeriods) {
			return r, os.ErrNotExist
		}
		r.itemAt = 5
		if len(parts) == 5 {
			r.node = "rankings-period"
		} else {
			r.node = "movies"
		}
		return r, nil
	case "actors", "series", "makers":
		if len(parts) == 2 {
			r.node = "entities-root"
			return r, nil
		}
		r.typ = parts[2]
		if r.section == "actors" {
			if !inBrowseList(r.typ, []string{"recommend", "censored", "uncensored"}) {
				return r, os.ErrNotExist
			}
		} else if !inBrowseList(r.typ, davMovieTypes) {
			return r, os.ErrNotExist
		}
		if len(parts) == 3 {
			r.node = "entities-type"
			return r, nil
		}
		r.entityName = parts[3]
		var ok bool
		r.entityID, ok = davLabelID(r.entityName)
		if !ok {
			return r, os.ErrNotExist
		}
		r.itemAt = 4
		if len(parts) == 4 {
			r.node = "entities-person"
		} else {
			r.node = "movies"
		}
		return r, nil
	}
	return r, os.ErrNotExist
}

func (d *davServer) browseLookup(ctx context.Context, catalog davCatalog, parts []string, fullPath string) (davNode, error) {
	r, err := parseDAVBrowse(parts)
	if err != nil {
		return davNode{}, err
	}
	// Playlist tokens retain their original parent scope even when an entity's
	// ordinal changes in a refreshed directory listing.
	if r.itemAt > 0 && len(parts) > r.itemAt {
		tail := parts[r.itemAt:]
		if len(tail) >= 2 && tail[1] == "media" {
			return d.tokenNode(ctx, tail, 2, fullPath)
		}
		if len(tail) >= 3 && tail[1] == "stream" && tail[2] == "media" {
			return d.tokenNode(ctx, tail, 3, fullPath)
		}
	}
	if r.section == "actors" || r.section == "series" || r.section == "makers" {
		if r.entityID != "" {
			parent := path.Join("/", strings.Join(parts[:3], "/"))
			entities, err := d.flatEntities(ctx, parent, catalog, r.section, r.typ)
			if err != nil {
				return davNode{}, err
			}
			if entity, ok := entities.entities[r.entityName]; !ok || entity.ID != r.entityID {
				return davNode{}, os.ErrNotExist
			}
		}
	}
	if r.itemAt > 0 && len(parts) > r.itemAt {
		dir := flatParent(fullPath, parts, r.itemAt)
		return d.flatItemNode(ctx, catalog, parts[r.itemAt:], fullPath, func() (*flatSnapshot, error) {
			return d.flatBrowseMovies(ctx, catalog, dir, r)
		})
	}
	modTime := d.startedAt
	switch r.node {
	case "categories-type", "categories-tag", "rankings-period", "entities-person":
		snapshot, err := d.flatBrowseMovies(ctx, catalog, fullPath, r)
		if err != nil {
			return davNode{}, err
		}
		modTime = snapshot.fetchedAt
	case "rankings-type":
		if r.kind == "top250" {
			snapshot, err := d.flatBrowseMovies(ctx, catalog, fullPath, r)
			if err != nil {
				return davNode{}, err
			}
			modTime = snapshot.fetchedAt
		}
	case "entities-type":
		snapshot, err := d.flatEntities(ctx, fullPath, catalog, r.section, r.typ)
		if err != nil {
			return davNode{}, err
		}
		modTime = snapshot.fetchedAt
	case "categories-tags", "categories-group":
		dir := fullPath
		if r.node == "categories-group" {
			dir = path.Dir(fullPath)
		}
		snapshot, err := d.flatTags(ctx, dir, catalog, r.typ)
		if err != nil {
			return davNode{}, err
		}
		if r.node == "categories-group" {
			found := false
			for _, group := range snapshot.tags {
				if group.ID == r.groupID && davReadableName(group.Name, group.ID) == parts[4] {
					found = true
					break
				}
			}
			if !found {
				return davNode{}, os.ErrNotExist
			}
		}
		modTime = snapshot.fetchedAt
	}
	return davNode{name: parts[len(parts)-1], dir: true, modTime: modTime}, nil
}

func (d *davServer) browseChildren(ctx context.Context, parts []string) ([]davNode, error) {
	r, err := parseDAVBrowse(parts)
	if err != nil {
		return nil, err
	}
	catalog := d.catalogs[parts[0]]
	name := path.Join("/", strings.Join(parts, "/"))
	switch r.node {
	case "categories-root":
		return davDirs(davMovieTypes), nil
	case "categories-type":
		snapshot, err := d.flatBrowseMovies(ctx, catalog, name, r)
		if err != nil {
			return nil, err
		}
		out := append([]davNode(nil), snapshot.nodes...)
		return append(out, davNode{name: "tags", dir: true, modTime: snapshot.fetchedAt}), nil
	case "categories-tags":
		snapshot, err := d.flatTags(ctx, name, catalog, r.typ)
		if err != nil {
			return nil, err
		}
		return snapshot.nodes, nil
	case "categories-group":
		snapshot, err := d.flatTags(ctx, path.Dir(name), catalog, r.typ)
		if err != nil {
			return nil, err
		}
		for _, group := range snapshot.tags {
			if group.ID == r.groupID && davReadableName(group.Name, group.ID) == parts[4] {
				out := make([]davNode, 0, len(group.Tags))
				for _, tag := range group.Tags {
					out = append(out, davNode{name: davReadableName(tag.Name, tag.ID), dir: true, modTime: snapshot.fetchedAt})
				}
				return out, nil
			}
		}
		return nil, os.ErrNotExist
	case "categories-tag", "rankings-period", "entities-person":
		snapshot, err := d.flatBrowseMovies(ctx, catalog, name, r)
		if err != nil {
			return nil, err
		}
		return snapshot.nodes, nil
	case "rankings-root":
		return davDirs([]string{"playback", "score", "top250"}), nil
	case "rankings-kind":
		if r.kind == "playback" {
			return davDirs(davPlaybackTypes), nil
		}
		if r.kind == "score" {
			return davDirs(davMovieTypes), nil
		}
		return davDirs(davRankingTypes), nil
	case "rankings-type":
		if r.kind == "top250" {
			snapshot, err := d.flatBrowseMovies(ctx, catalog, name, r)
			if err != nil {
				return nil, err
			}
			return snapshot.nodes, nil
		}
		return davDirs(davPeriods), nil
	case "entities-root":
		if r.section == "actors" {
			return davDirs([]string{"recommend", "censored", "uncensored"}), nil
		}
		return davDirs(davMovieTypes), nil
	case "entities-type":
		snapshot, err := d.flatEntities(ctx, name, catalog, r.section, r.typ)
		if err != nil {
			return nil, err
		}
		return snapshot.nodes, nil
	}
	return nil, os.ErrNotExist
}
