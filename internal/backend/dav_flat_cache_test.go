package backend

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/fdkevin0/MetaDonalds/internal/core"
	"github.com/fdkevin0/MetaDonalds/internal/plugins/javdb"
)

const testFlatKey = "movies:/catalog/latest"

func testFlatSnapshot(id string, age time.Duration) *flatSnapshot {
	s := flatMovieSnapshot([]core.Item{{ID: id, Code: id}})
	s.fetchedAt = time.Now().UTC().Add(-age)
	s.expiresAt = s.fetchedAt.Add(flatTTL)
	return s
}

func waitFlatFlight(t *testing.T, c *flatCache, key string) {
	t.Helper()
	c.mu.Lock()
	f := c.flights[key]
	c.mu.Unlock()
	if f == nil {
		return
	}
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
		t.Fatal("cache flight did not finish")
	}
}

func TestFlatSWRCoalescesAndPinsRequest(t *testing.T) {
	var c flatCache
	t.Cleanup(c.close)
	old := testFlatSnapshot("old", 6*time.Minute)
	c.storeLocked(testFlatKey, old)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	fetch := func(ctx context.Context) (*flatSnapshot, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return testFlatSnapshot("new", 0), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ctx := context.WithValue(context.Background(), davMemoKey{}, &davMemo{})
	got, err := c.load(ctx, testFlatKey, fetch)
	if err != nil || got != old {
		t.Fatalf("stale read: %v %v", got, err)
	}
	<-started
	for range 10 {
		got, err = c.load(context.Background(), testFlatKey, fetch)
		if err != nil || got != old {
			t.Fatal("stale read blocked or changed")
		}
	}
	close(release)
	waitFlatFlight(t, &c, testFlatKey)
	got, err = c.load(ctx, testFlatKey, fetch)
	if err != nil || got != old {
		t.Fatal("request mixed snapshot generations")
	}
	got, err = c.load(context.Background(), testFlatKey, fetch)
	if err != nil || got.items["0001-new"].ID != "new" || calls.Load() != 1 {
		t.Fatalf("refresh: %v, calls %d", err, calls.Load())
	}
}

func TestFlatFailedRefreshRetainsOldAndBacksOff(t *testing.T) {
	var c flatCache
	t.Cleanup(c.close)
	old := testFlatSnapshot("old", 6*time.Minute)
	c.storeLocked(testFlatKey, old)
	var calls atomic.Int32
	fetch := func(context.Context) (*flatSnapshot, error) { calls.Add(1); return nil, errors.New("page 2 failed") }
	got, err := c.load(context.Background(), testFlatKey, fetch)
	if err != nil || got != old {
		t.Fatal("failed to serve stale snapshot")
	}
	waitFlatFlight(t, &c, testFlatKey)
	got, err = c.load(context.Background(), testFlatKey, fetch)
	if err != nil || got != old || calls.Load() != 1 {
		t.Fatal("failure replaced snapshot or bypassed backoff")
	}
	if key := c.due(time.Now()); key != "" {
		t.Fatal("scheduler ignored backoff")
	}
	c.mu.Lock()
	old.fetchedAt = time.Now().Add(-flatMaxAge)
	c.mu.Unlock()
	if _, err = c.load(context.Background(), testFlatKey, fetch); err == nil {
		t.Fatal("served snapshot beyond maximum age")
	}
}

func TestFlatCanceledWaiterAndShutdown(t *testing.T) {
	var c flatCache
	started, release := make(chan struct{}), make(chan struct{})
	fetch := func(ctx context.Context) (*flatSnapshot, error) {
		close(started)
		select {
		case <-release:
			return testFlatSnapshot("new", 0), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := c.load(ctx, testFlatKey, fetch); result <- err }()
	<-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	waitFlatFlight(t, &c, testFlatKey)
	if _, err := c.load(context.Background(), testFlatKey, fetch); err != nil {
		t.Fatal(err)
	}
	c.close()
	if _, err := c.load(context.Background(), testFlatKey, fetch); !errors.Is(err, context.Canceled) {
		t.Fatal("closed cache accepted work")
	}

	var canceled flatCache
	started = make(chan struct{})
	go func() {
		_, err := canceled.load(context.Background(), testFlatKey, func(ctx context.Context) (*flatSnapshot, error) { close(started); <-ctx.Done(); return nil, ctx.Err() })
		result <- err
	}()
	<-started
	canceled.close()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestFlatSchedulerRefreshesWithoutTrafficAndStopsWhenInactive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &davCatalogFake{media: javdb.Media{ID: "new", Number: "new"}}
		d := newDAV(map[string]davCatalog{"catalog": fake}, nil, "").(*davServer)
		d.startCache(DAVCacheConfig{}, nil, nil)
		defer d.flat.close()
		old := testFlatSnapshot("old", 6*time.Minute)
		d.flat.mu.Lock()
		d.flat.storeLocked(testFlatKey, old)
		accessed := time.Now()
		d.flat.activity[testFlatKey] = flatActivity{accessed: accessed}
		d.flat.mu.Unlock()
		time.Sleep(flatTick)
		synctest.Wait()
		d.flat.mu.Lock()
		got := d.flat.entries[testFlatKey]
		activity := d.flat.activity[testFlatKey]
		d.flat.mu.Unlock()
		if got == old || got.items["0001-new"].ID != "new" {
			t.Fatal("timer did not refresh")
		}
		if !activity.accessed.Equal(accessed) {
			t.Fatal("background refresh counted as user activity")
		}
		time.Sleep(flatActiveFor + flatTTL)
		synctest.Wait()
		if key := d.flat.due(time.Now()); key != "" {
			t.Fatal("inactive directory scheduled")
		}
	})
}

func TestFlatLoaderRejectsInvalidPaths(t *testing.T) {
	d := newDAV(map[string]davCatalog{"catalog": &davCatalogFake{}}, nil, "").(*davServer)
	for _, key := range []string{"movies:https://evil.test", "movies:/catalog/latest/../latest", "movies:/unknown/latest", "movies:/catalog/latest/0001-foo", "tags:/catalog/latest", "movies:/catalog/categories/invalid"} {
		if _, err := d.flatLoader(key); err == nil {
			t.Fatalf("accepted invalid refresh descriptor %q", key)
		}
	}
}
