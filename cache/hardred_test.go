package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- Hard Red: GetOrFetch concurrent same key ---

func TestHR_GetOrFetch_ConcurrentSameKey_100(t *testing.T) {
	ctx := context.Background()
	l1 := NewMemory(ctx)
	tiered := NewTiered(l1, nil, time.Minute)

	var fetchCount atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			val, err := tiered.GetOrFetch(ctx, "hot-key", time.Minute, func(_ context.Context) ([]byte, error) {
				fetchCount.Add(1)
				time.Sleep(5 * time.Millisecond)
				return []byte("result"), nil
			})
			if err != nil {
				t.Errorf("GetOrFetch: %v", err)
				return
			}
			if string(val) != "result" {
				t.Errorf("value = %q", val)
			}
		}()
	}
	wg.Wait()

	// Singleflight + cache: fetch should execute very few times.
	if fetchCount.Load() > 3 {
		t.Errorf("fetchCount = %d, want <= 3 (singleflight should deduplicate)", fetchCount.Load())
	}
}

// --- Hard Red: GetOrFetch concurrent different keys ---

func TestHR_GetOrFetch_ConcurrentDifferentKeys(t *testing.T) {
	ctx := context.Background()
	l1 := NewMemory(ctx)
	tiered := NewTiered(l1, nil, time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := "key-" + string(rune('A'+idx%26))
			val, err := tiered.GetOrFetch(ctx, key, time.Minute, func(_ context.Context) ([]byte, error) {
				return []byte("val-" + key), nil
			})
			if err != nil {
				t.Errorf("GetOrFetch(%s): %v", key, err)
				return
			}
			if len(val) == 0 {
				t.Errorf("empty value for key %s", key)
			}
		}(i)
	}
	wg.Wait()
}

// --- Hard Red: GetOrFetch error propagation to all waiters ---

func TestHR_GetOrFetch_ErrorPropagation(t *testing.T) {
	ctx := context.Background()
	l1 := NewMemory(ctx)
	tiered := NewTiered(l1, nil, time.Minute)

	wantErr := errors.New("fetch boom")
	var errCount atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := tiered.GetOrFetch(ctx, "err-key", time.Minute, func(_ context.Context) ([]byte, error) {
				time.Sleep(5 * time.Millisecond)
				return nil, wantErr
			})
			if err != nil {
				errCount.Add(1)
			}
		}()
	}
	wg.Wait()

	// All goroutines should see the error.
	if errCount.Load() != 50 {
		t.Errorf("errCount = %d, want 50 (all waiters should get error)", errCount.Load())
	}

	// Key should NOT be cached after error.
	_, ok := tiered.Get(ctx, "err-key")
	if ok {
		t.Error("failed fetch should not be cached")
	}
}

// --- Hard Red: GetOrFetch with nil bytes return ---

func TestHR_GetOrFetch_NilBytesReturn(t *testing.T) {
	ctx := context.Background()
	l1 := NewMemory(ctx)
	tiered := NewTiered(l1, nil, time.Minute)

	// fetchFn returns nil bytes (not error) — should this cache?
	val, err := tiered.GetOrFetch(ctx, "nil-val", time.Minute, func(_ context.Context) ([]byte, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != nil {
		t.Errorf("expected nil value, got %q", val)
	}
}

// --- Hard Red: GetOrFetch with cancelled context ---

func TestHR_GetOrFetch_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	l1 := NewMemory(ctx)
	tiered := NewTiered(l1, nil, time.Minute)
	cancel()

	_, err := tiered.GetOrFetch(ctx, "cancelled", time.Minute, func(ctx context.Context) ([]byte, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return []byte("ok"), nil
		}
	})
	// The fetchFn may or may not see the cancellation, but no panic expected.
	_ = err
}

// --- Hard Red: concurrent Set + Get (race detector check) ---

func TestHR_Memory_ConcurrentSetGet(t *testing.T) {
	ctx := context.Background()
	m := NewMemory(ctx)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = m.Set(ctx, "race-key", []byte("value"), time.Minute)
		}()
		go func() {
			defer wg.Done()
			m.Get(ctx, "race-key")
		}()
	}
	wg.Wait()
}

// --- Hard Red: Tiered with nil L2 concurrent access ---

func TestHR_Tiered_NilL2_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	l1 := NewMemory(ctx)
	tiered := NewTiered(l1, nil, time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_ = tiered.Set(ctx, "key", []byte("v"), time.Minute)
		}()
		go func() {
			defer wg.Done()
			tiered.Get(ctx, "key")
		}()
		go func() {
			defer wg.Done()
			_, _ = tiered.GetOrFetch(ctx, "key", time.Minute, func(_ context.Context) ([]byte, error) {
				return []byte("fetched"), nil
			})
		}()
	}
	wg.Wait()
}
