package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestKey(t *testing.T) {
	k1 := Key("gs", "search", "hello")
	k2 := Key("gs", "search", "hello")
	k3 := Key("gs", "search", "world")

	if k1 != k2 {
		t.Errorf("same parts produced different keys: %q vs %q", k1, k2)
	}
	if k1 == k3 {
		t.Error("different parts produced same key")
	}
	if len(k1) < 10 {
		t.Errorf("key too short: %q", k1)
	}
}

func TestMemory_GetSet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewMemory(ctx)

	// Miss.
	_, ok := m.Get(ctx, "missing")
	if ok {
		t.Fatal("expected miss for unknown key")
	}

	// Set and hit.
	if err := m.Set(ctx, "k1", []byte("value1"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	data, ok := m.Get(ctx, "k1")
	if !ok {
		t.Fatal("expected hit for k1")
	}
	if string(data) != "value1" {
		t.Errorf("Get(k1) = %q, want %q", data, "value1")
	}

	// Stats.
	hits, misses := m.Stats()
	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
	if misses != 1 {
		t.Errorf("misses = %d, want 1", misses)
	}
}

func TestMemory_Expiry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewMemory(ctx)

	if err := m.Set(ctx, "k1", []byte("v"), 1*time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	_, ok := m.Get(ctx, "k1")
	if ok {
		t.Error("expected miss for expired key")
	}
}

func TestMemory_Eviction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewMemory(ctx, WithMaxEntries(3))

	for i := range 5 {
		key := string(rune('a' + i))
		if err := m.Set(ctx, key, []byte(key), time.Minute); err != nil {
			t.Fatalf("Set(%s): %v", key, err)
		}
	}

	// Should have at most maxEntries.
	count := 0
	m.store.Range(func(_, _ any) bool {
		count++
		return true
	})
	if count > 3 {
		t.Errorf("after eviction, count = %d, want <= 3", count)
	}
}

func TestMemory_CopyOnGetSet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewMemory(ctx)

	original := []byte("hello")
	if err := m.Set(ctx, "k", original, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Mutate original — should not affect cached value.
	original[0] = 'X'

	data, ok := m.Get(ctx, "k")
	if !ok {
		t.Fatal("expected hit")
	}
	if string(data) != "hello" {
		t.Errorf("cached value mutated: got %q, want %q", data, "hello")
	}

	// Mutate returned value — should not affect cached value.
	data[0] = 'Y'

	data2, _ := m.Get(ctx, "k")
	if string(data2) != "hello" {
		t.Errorf("cached value mutated via Get: got %q, want %q", data2, "hello")
	}
}

func TestMemory_Concurrent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewMemory(ctx, WithMaxEntries(100))

	var wg sync.WaitGroup
	const goroutines = 50
	const iterations = 100

	wg.Add(goroutines)
	for i := range goroutines {
		go func() {
			defer wg.Done()
			key := string(rune('A' + (i % 26)))
			for range iterations {
				_ = m.Set(ctx, key, []byte("data"), time.Minute)
				m.Get(ctx, key)
			}
		}()
	}
	wg.Wait()

	hits, misses := m.Stats()
	if hits+misses == 0 {
		t.Error("expected some cache operations")
	}
}

func TestRedis_GetSet(t *testing.T) {
	mr := miniredis.RunT(t)

	r := NewRedis("redis://" + mr.Addr())
	if r == nil {
		t.Fatal("NewRedis returned nil")
	}

	ctx := context.Background()

	// Miss.
	_, ok := r.Get(ctx, "missing")
	if ok {
		t.Fatal("expected miss for unknown key")
	}

	// Set and hit.
	if err := r.Set(ctx, "k1", []byte("val"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	data, ok := r.Get(ctx, "k1")
	if !ok {
		t.Fatal("expected hit for k1")
	}
	if string(data) != "val" {
		t.Errorf("Get(k1) = %q, want %q", data, "val")
	}

	// Stats.
	hits, misses := r.Stats()
	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
	if misses != 1 {
		t.Errorf("misses = %d, want 1", misses)
	}
}

func TestRedis_TTL(t *testing.T) {
	mr := miniredis.RunT(t)

	r := NewRedis("redis://" + mr.Addr())
	if r == nil {
		t.Fatal("NewRedis returned nil")
	}

	ctx := context.Background()

	if err := r.Set(ctx, "k1", []byte("v"), 100*time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Fast-forward miniredis time.
	mr.FastForward(200 * time.Millisecond)

	_, ok := r.Get(ctx, "k1")
	if ok {
		t.Error("expected miss for expired key")
	}
}

func TestRedis_InvalidURL(t *testing.T) {
	r := NewRedis("not-a-url")
	if r != nil {
		t.Error("expected nil for invalid URL")
	}
}

func TestRedis_Unreachable(t *testing.T) {
	r := NewRedis("redis://127.0.0.1:1") // port 1 is almost certainly not Redis
	if r != nil {
		t.Error("expected nil for unreachable Redis")
	}
}

func TestTiered_L1Hit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l1 := NewMemory(ctx)
	mr := miniredis.RunT(t)
	l2 := NewRedis("redis://" + mr.Addr())

	tc := NewTiered(l1, l2, time.Minute)

	// Set only in L1.
	_ = l1.Set(ctx, "k", []byte("from-l1"), time.Minute)

	data, ok := tc.Get(ctx, "k")
	if !ok {
		t.Fatal("expected L1 hit")
	}
	if string(data) != "from-l1" {
		t.Errorf("got %q, want %q", data, "from-l1")
	}
}

func TestTiered_L2HitPromotesToL1(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l1 := NewMemory(ctx)
	mr := miniredis.RunT(t)
	l2 := NewRedis("redis://" + mr.Addr())

	tc := NewTiered(l1, l2, time.Minute)

	// Set only in L2.
	_ = l2.Set(ctx, "k", []byte("from-l2"), time.Minute)

	data, ok := tc.Get(ctx, "k")
	if !ok {
		t.Fatal("expected L2 hit")
	}
	if string(data) != "from-l2" {
		t.Errorf("got %q, want %q", data, "from-l2")
	}

	// Now should be in L1.
	data2, ok2 := l1.Get(ctx, "k")
	if !ok2 {
		t.Fatal("expected L1 promotion")
	}
	if string(data2) != "from-l2" {
		t.Errorf("L1 promoted value = %q, want %q", data2, "from-l2")
	}
}

func TestTiered_SetWritesBoth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l1 := NewMemory(ctx)
	mr := miniredis.RunT(t)
	l2 := NewRedis("redis://" + mr.Addr())

	tc := NewTiered(l1, l2, time.Minute)

	if err := tc.Set(ctx, "k", []byte("val"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Check L1.
	d1, ok := l1.Get(ctx, "k")
	if !ok || string(d1) != "val" {
		t.Errorf("L1: got %q/%v, want %q/true", d1, ok, "val")
	}

	// Check L2.
	d2, ok := l2.Get(ctx, "k")
	if !ok || string(d2) != "val" {
		t.Errorf("L2: got %q/%v, want %q/true", d2, ok, "val")
	}
}

func TestTiered_L2Nil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l1 := NewMemory(ctx)
	tc := NewTiered(l1, nil, time.Minute)

	if err := tc.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}

	data, ok := tc.Get(ctx, "k")
	if !ok || string(data) != "v" {
		t.Errorf("L1-only Get: %q/%v, want %q/true", data, ok, "v")
	}
}

func TestTiered_Stats(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l1 := NewMemory(ctx)
	mr := miniredis.RunT(t)
	l2 := NewRedis("redis://" + mr.Addr())

	tc := NewTiered(l1, l2, time.Minute)

	_ = tc.Set(ctx, "k", []byte("v"), time.Minute)
	tc.Get(ctx, "k")    // L1 hit
	tc.Get(ctx, "miss") // total miss

	hits, misses := tc.Stats()
	if hits < 1 {
		t.Errorf("hits = %d, want >= 1", hits)
	}
	if misses < 1 {
		t.Errorf("misses = %d, want >= 1", misses)
	}
}

func TestTiered_GetOrFetch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l1 := NewMemory(ctx)
	tiered := NewTiered(l1, nil, time.Minute)

	var fetchCount atomic.Int64
	val, err := tiered.GetOrFetch(ctx, "key1", time.Minute, func(_ context.Context) ([]byte, error) {
		fetchCount.Add(1)
		return []byte("fetched-value"), nil
	})
	if err != nil {
		t.Fatalf("GetOrFetch: %v", err)
	}
	if string(val) != "fetched-value" {
		t.Errorf("value = %q, want %q", val, "fetched-value")
	}
	if fetchCount.Load() != 1 {
		t.Errorf("fetch count = %d, want 1", fetchCount.Load())
	}

	// Second call should hit cache.
	val2, err := tiered.GetOrFetch(ctx, "key1", time.Minute, func(_ context.Context) ([]byte, error) {
		fetchCount.Add(1)
		return []byte("should-not-run"), nil
	})
	if err != nil {
		t.Fatalf("GetOrFetch (cached): %v", err)
	}
	if string(val2) != "fetched-value" {
		t.Errorf("cached value = %q, want %q", val2, "fetched-value")
	}
	if fetchCount.Load() != 1 {
		t.Errorf("fetch count = %d, want 1 (cached)", fetchCount.Load())
	}
}

func TestTiered_GetOrFetch_Singleflight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l1 := NewMemory(ctx)
	tiered := NewTiered(l1, nil, time.Minute)

	var fetchCount atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			val, err := tiered.GetOrFetch(ctx, "shared-key", time.Minute, func(_ context.Context) ([]byte, error) {
				fetchCount.Add(1)
				time.Sleep(10 * time.Millisecond)
				return []byte("shared-value"), nil
			})
			if err != nil {
				t.Errorf("GetOrFetch: %v", err)
				return
			}
			if string(val) != "shared-value" {
				t.Errorf("value = %q", val)
			}
		}()
	}
	wg.Wait()

	if fetchCount.Load() > 2 {
		t.Errorf("fetch count = %d, want <= 2 (singleflight should deduplicate)", fetchCount.Load())
	}
}

func TestTiered_GetOrFetch_Error(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l1 := NewMemory(ctx)
	tiered := NewTiered(l1, nil, time.Minute)

	_, err := tiered.GetOrFetch(ctx, "err-key", time.Minute, func(_ context.Context) ([]byte, error) {
		return nil, errors.New("fetch failed")
	})
	if err == nil {
		t.Fatal("expected error")
	}

	_, ok := tiered.Get(ctx, "err-key")
	if ok {
		t.Error("failed fetch should not be cached")
	}
}
