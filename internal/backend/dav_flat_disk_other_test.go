//go:build !unix

package backend

import (
	"context"
	"testing"

	"github.com/fdkevin0/MetaDonalds/internal/plugins/javdb"
)

func TestFlatPersistenceUnavailableKeepsMemory(t *testing.T) {
	d := newDAV(map[string]davCatalog{"catalog": &davCatalogFake{media: javdb.Media{ID: "movie", Number: "CODE"}}}, nil, "").(*davServer)
	refresh := false
	d.startCache(DAVCacheConfig{Dir: t.TempDir(), Refresh: &refresh}, map[string]string{"catalog": "source"}, nil)
	t.Cleanup(d.flat.close)
	if d.flat.disk != nil {
		t.Fatal("persistence enabled on unsupported platform")
	}
	load, err := d.flatLoader(testFlatKey)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := load(context.Background())
	if err != nil || snapshot == nil || snapshot.items["0001-CODE"].ID != "movie" {
		t.Fatalf("memory fallback failed: %v %v", snapshot, err)
	}
	if d.flat.entries[testFlatKey] != snapshot {
		t.Fatal("fallback discarded memory snapshot")
	}
}
