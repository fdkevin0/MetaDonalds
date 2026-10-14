package backend

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/core"
	"github.com/fdkevin0/MetaDonalds/internal/plugins/javdb"
)

const flatDiskVersion = 1

type flatRecord struct {
	Version               int
	Key, Source           string
	FetchedAt, AccessedAt time.Time
	Movies                []core.Item
	Entities              []javdb.BrowseEntity
	Tags                  []javdb.BrowseTagGroup
}

type flatDisk struct {
	mu           sync.Mutex
	dir          string
	lock         *os.File
	fingerprints map[string]string
}

func openFlatDisk(dir string, fingerprints map[string]string) (*flatDisk, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	lock, err := lockFlatDisk(filepath.Join(dir, "lock"))
	if err != nil {
		return nil, err
	}
	return &flatDisk{dir: dir, lock: lock, fingerprints: fingerprints}, nil
}

func (d *flatDisk) close() { _ = d.lock.Close() }

func (d *flatDisk) source(key string) string {
	_, name, _ := strings.Cut(key, ":")
	id, _, _ := strings.Cut(strings.TrimPrefix(name, "/"), "/")
	return d.fingerprints[id]
}

func flatFilename(key string) string { return fmt.Sprintf("%x.json", sha256.Sum256([]byte(key))) }

func (d *flatDisk) write(key string, snapshot *flatSnapshot, accessed time.Time) error {
	r := flatRecord{Version: flatDiskVersion, Key: key, Source: d.source(key), FetchedAt: snapshot.fetchedAt, AccessedAt: accessed, Tags: snapshot.tags}
	for _, n := range snapshot.nodes {
		if snapshot.items != nil {
			r.Movies = append(r.Movies, snapshot.items[n.name])
		}
		if snapshot.entities != nil {
			r.Entities = append(r.Entities, snapshot.entities[n.name])
		}
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(data) > flatMaxBytes {
		return errFlatListLimit
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	f, err := os.CreateTemp(d.dir, ".snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), filepath.Join(d.dir, flatFilename(key))); err != nil {
		return err
	}
	return d.prune()
}

// The disk budget matches the small in-memory directory budget.
func (d *flatDisk) prune() error {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return err
	}
	type file struct {
		name     string
		size     int64
		modified time.Time
	}
	var files []file
	var total int64
	for _, e := range entries {
		if !isFlatFilename(e.Name()) || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		files = append(files, file{e.Name(), info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modified.Before(files[j].modified) })
	for len(files) > flatMaxLists || total > flatMaxBytes {
		if err := os.Remove(filepath.Join(d.dir, files[0].name)); err != nil {
			return err
		}
		total -= files[0].size
		files = files[1:]
	}
	return nil
}

func (d *flatDisk) restore(server *davServer) {
	if err := d.prune(); err != nil {
		server.flat.logger.Warn("DAV cache restore failed", "operation", "prune", "error", err)
		return
	}
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		server.flat.logger.Warn("DAV cache restore failed", "operation", "read", "error", err)
		return
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		filename := filepath.Join(d.dir, e.Name())
		if strings.HasPrefix(e.Name(), ".snapshot-") {
			_ = os.Remove(filename)
			continue
		}
		if !isFlatFilename(e.Name()) {
			continue
		}
		r, snapshot, err := d.read(filename)
		if err == nil {
			_, err = server.flatLoader(r.Key)
		}
		if err != nil || flatFilename(r.Key) != e.Name() {
			_ = os.Remove(filename)
			continue
		}
		server.flat.storeLocked(r.Key, snapshot)
		server.flat.activity[r.Key] = flatActivity{accessed: r.AccessedAt}
	}
}

func (d *flatDisk) read(filename string) (flatRecord, *flatSnapshot, error) {
	var r flatRecord
	f, err := os.Open(filename)
	if err != nil {
		return r, nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, flatMaxBytes+1))
	if err != nil {
		return r, nil, err
	}
	if len(data) > flatMaxBytes || json.Unmarshal(data, &r) != nil || r.Version != flatDiskVersion || r.Source == "" || r.Source != d.source(r.Key) || r.FetchedAt.After(time.Now()) || time.Since(r.FetchedAt) >= flatMaxAge || r.AccessedAt.After(time.Now()) {
		return r, nil, os.ErrInvalid
	}
	var snapshot *flatSnapshot
	seen := map[string]bool{}
	validID := func(id string) bool {
		if !davID.MatchString(id) || seen[id] {
			return false
		}
		seen[id] = true
		return true
	}
	switch {
	case strings.HasPrefix(r.Key, "movies:"):
		if len(r.Movies) > flatMaxItems || len(r.Entities) != 0 || len(r.Tags) != 0 {
			return r, nil, os.ErrInvalid
		}
		for _, item := range r.Movies {
			if !validID(item.ID) {
				return r, nil, os.ErrInvalid
			}
		}
		snapshot = flatMovieSnapshot(r.Movies)
	case strings.HasPrefix(r.Key, "entities:"):
		if len(r.Entities) > flatMaxItems || len(r.Movies) != 0 || len(r.Tags) != 0 {
			return r, nil, os.ErrInvalid
		}
		for _, entity := range r.Entities {
			if !validID(entity.ID) {
				return r, nil, os.ErrInvalid
			}
		}
		snapshot = flatEntitySnapshot(r.Entities)
	case strings.HasPrefix(r.Key, "tags:"):
		if len(r.Tags) > flatMaxItems || len(r.Movies) != 0 || len(r.Entities) != 0 {
			return r, nil, os.ErrInvalid
		}
		for _, group := range r.Tags {
			if !validID(group.ID) {
				return r, nil, os.ErrInvalid
			}
			for _, tag := range group.Tags {
				if !davID.MatchString(tag.ID) {
					return r, nil, os.ErrInvalid
				}
			}
		}
		snapshot = flatTagSnapshot(r.Tags)
	default:
		return r, nil, os.ErrInvalid
	}
	if snapshot.bytes > flatMaxBytes {
		return r, nil, errFlatListLimit
	}
	snapshot.fetchedAt, snapshot.expiresAt = r.FetchedAt, r.FetchedAt.Add(flatTTL)
	for i := range snapshot.nodes {
		if snapshot.nodes[i].modTime.IsZero() {
			snapshot.nodes[i].modTime = r.FetchedAt
		}
	}
	return r, snapshot, nil
}

func isFlatFilename(name string) bool {
	if len(name) != 69 || !strings.HasSuffix(name, ".json") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimSuffix(name, ".json"))
	return err == nil
}
