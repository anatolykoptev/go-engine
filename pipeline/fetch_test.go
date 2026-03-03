package pipeline

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestParallelFetchV2_Basic(t *testing.T) {
	urls := []string{"http://a.com", "http://b.com", "http://c.com"}
	results := ParallelFetch(context.Background(), urls,
		func(_ context.Context, url string) (string, error) {
			return "content-" + url, nil
		})
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("unexpected error for %s: %v", r.URL, r.Err)
		}
		if r.Content == "" {
			t.Errorf("empty content for %s", r.URL)
		}
	}
}

func TestParallelFetchV2_PreservesErrors(t *testing.T) {
	urls := []string{"http://ok.com", "http://fail.com"}
	results := ParallelFetch(context.Background(), urls,
		func(_ context.Context, url string) (string, error) {
			if url == "http://fail.com" {
				return "", errors.New("fetch failed")
			}
			return "ok", nil
		})
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	var errs, oks int
	for _, r := range results {
		if r.Err != nil {
			errs++
		} else {
			oks++
		}
	}
	if errs != 1 || oks != 1 {
		t.Errorf("errs=%d oks=%d, want errs=1 oks=1", errs, oks)
	}
}

func TestParallelFetchV2_BoundedConcurrency(t *testing.T) {
	var active, maxActive atomic.Int64
	urls := make([]string, 20)
	for i := range urls {
		urls[i] = "http://example.com/" + string(rune('a'+i))
	}
	results := ParallelFetch(context.Background(), urls,
		func(_ context.Context, _ string) (string, error) {
			cur := active.Add(1)
			for {
				old := maxActive.Load()
				if cur <= old || maxActive.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			active.Add(-1)
			return "ok", nil
		},
		WithMaxConcurrency(5))
	if len(results) != 20 {
		t.Fatalf("results = %d, want 20", len(results))
	}
	if maxActive.Load() > 5 {
		t.Errorf("max concurrent = %d, want <= 5", maxActive.Load())
	}
}

func TestParallelFetchV2_Empty(t *testing.T) {
	results := ParallelFetch(context.Background(), nil,
		func(_ context.Context, _ string) (string, error) { return "ok", nil })
	if len(results) != 0 {
		t.Errorf("results = %d, want 0", len(results))
	}
}

func TestParallelFetchV2_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	urls := []string{"http://a.com", "http://b.com"}
	cancel()
	results := ParallelFetch(ctx, urls,
		func(ctx context.Context, _ string) (string, error) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Second):
				return "ok", nil
			}
		})
	for _, r := range results {
		if r.Err == nil {
			t.Errorf("expected error for %s with cancelled context", r.URL)
		}
	}
}
