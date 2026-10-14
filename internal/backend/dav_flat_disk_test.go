//go:build unix

package backend

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/plugins/javdb"
)

func TestFlatPersistenceRoundTripAndSourceIsolation(t *testing.T) {
	dir := t.TempDir()
	fake := &davCatalogFake{media: javdb.Media{ID: "movie", Number: "CODE"}}
	refresh := false
	newServer := func(source string) *davServer {
		d := newDAV(map[string]davCatalog{"catalog": fake}, nil, "").(*davServer)
		d.startCache(DAVCacheConfig{Dir: dir, Refresh: &refresh}, map[string]string{"catalog": source}, nil)
		t.Cleanup(d.flat.close)
		return d
	}
	d := newServer("original")
	keys := []string{testFlatKey, "entities:/catalog/actors/censored", "tags:/catalog/categories/censored/tags"}
	for _, key := range keys {
		load, err := d.flatLoader(key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := load(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	originalTime := d.flat.entries[testFlatKey].fetchedAt
	d.flat.close()
	restored := newServer("original")
	for _, key := range keys {
		if restored.flat.entries[key] == nil {
			t.Fatalf("missing restored %s", key)
		}
	}
	if !restored.flat.entries[testFlatKey].fetchedAt.Equal(originalTime) {
		t.Fatal("restart renewed freshness")
	}
	if restored.flat.entries[keys[1]].entities["0001-"+davReadableName("演员 A/B", "actor1")].ID != "actor1" {
		t.Fatal("entity mapping lost")
	}
	if len(restored.flat.entries[keys[2]].tags) != 1 {
		t.Fatal("tag groups lost")
	}
	blocked := newServer("original")
	if blocked.flat.disk != nil {
		t.Fatal("second writer acquired cache directory")
	}
	blocked.flat.close()
	restored.flat.close()
	changed := newServer("changed")
	if len(changed.flat.entries) != 0 {
		t.Fatal("reused cache from different upstream")
	}
}

func TestFlatDiskRejectsCorruptionAndExpiry(t *testing.T) {
	dir := t.TempDir()
	disk, err := openFlatDisk(dir, map[string]string{"catalog": "source"})
	if err != nil {
		t.Fatal(err)
	}
	defer disk.close()
	old := testFlatSnapshot("old", 6*time.Minute)
	if err := disk.write(testFlatKey, old, time.Now()); err != nil {
		t.Fatal(err)
	}
	_, got, err := disk.read(filepath.Join(dir, flatFilename(testFlatKey)))
	if err != nil || !got.expiresAt.Equal(old.expiresAt) {
		t.Fatal("stale deadline not preserved", err)
	}
	old.fetchedAt = time.Now().Add(-flatMaxAge)
	if err := disk.write(testFlatKey, old, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := disk.read(filepath.Join(dir, flatFilename(testFlatKey))); err == nil {
		t.Fatal("expired disk snapshot accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, flatFilename(testFlatKey)), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := disk.read(filepath.Join(dir, flatFilename(testFlatKey))); err == nil {
		t.Fatal("corrupt snapshot accepted")
	}
}

func TestFlatDiskBoundedAndWriteFailureKeepsMemory(t *testing.T) {
	dir := t.TempDir()
	refresh := false
	d := newDAV(map[string]davCatalog{"catalog": &davCatalogFake{media: javdb.Media{ID: "movie"}}}, nil, "").(*davServer)
	d.startCache(DAVCacheConfig{Dir: dir, Refresh: &refresh}, map[string]string{"catalog": "source"}, nil)
	t.Cleanup(d.flat.close)
	for i := range flatMaxLists + 3 {
		key := "movies:/catalog/search/" + time.Unix(int64(i), 0).Format("150405")
		if err := d.flat.disk.write(key, testFlatSnapshot("id", 0), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, f := range files {
		if isFlatFilename(f.Name()) {
			count++
		}
	}
	if count != flatMaxLists {
		t.Fatalf("disk has %d snapshots", count)
	}
	// A regular file at the configured directory simulates an unwritable store.
	broken := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(broken, nil, 0600); err != nil {
		t.Fatal(err)
	}
	d.flat.disk.dir = broken
	load, _ := d.flatLoader(testFlatKey)
	if _, err := load(context.Background()); err != nil {
		t.Fatalf("disk failure broke read: %v", err)
	}
	if d.flat.entries[testFlatKey] == nil {
		t.Fatal("disk failure discarded memory snapshot")
	}
}
