package cache

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkMemory_Get(b *testing.B) {
	ctx := context.Background()
	m := NewMemory(ctx)
	_ = m.Set(ctx, "bench-key", []byte("bench-value"), time.Hour)
	b.ResetTimer()
	for b.Loop() {
		m.Get(ctx, "bench-key")
	}
}

func BenchmarkMemory_Set(b *testing.B) {
	ctx := context.Background()
	m := NewMemory(ctx)
	val := []byte("bench-value")
	b.ResetTimer()
	for b.Loop() {
		_ = m.Set(ctx, "bench-key", val, time.Hour)
	}
}

func BenchmarkTiered_Get(b *testing.B) {
	ctx := context.Background()
	l1 := NewMemory(ctx)
	tiered := NewTiered(l1, nil, time.Hour)
	_ = tiered.Set(ctx, "bench-key", []byte("bench-value"), time.Hour)
	b.ResetTimer()
	for b.Loop() {
		tiered.Get(ctx, "bench-key")
	}
}

func BenchmarkTiered_GetOrFetch_Cached(b *testing.B) {
	ctx := context.Background()
	l1 := NewMemory(ctx)
	tiered := NewTiered(l1, nil, time.Hour)
	_, _ = tiered.GetOrFetch(ctx, "bench-key", time.Hour, func(_ context.Context) ([]byte, error) {
		return []byte("fetched"), nil
	})
	b.ResetTimer()
	for b.Loop() {
		_, _ = tiered.GetOrFetch(ctx, "bench-key", time.Hour, func(_ context.Context) ([]byte, error) {
			return []byte("should-not-run"), nil
		})
	}
}

func BenchmarkMemory_Set_ManyKeys(b *testing.B) {
	ctx := context.Background()
	m := NewMemory(ctx)
	val := []byte("v")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Set(ctx, fmt.Sprintf("key-%d", i), val, time.Hour)
	}
}
