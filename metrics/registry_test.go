package metrics

import (
	"strings"
	"sync"
	"testing"
)

func TestRegistry_IncrAndValue(t *testing.T) {
	r := New()

	if got := r.Value("unknown"); got != 0 {
		t.Errorf("Value(unknown) = %d, want 0", got)
	}

	r.Incr("requests")
	r.Incr("requests")
	r.Incr("requests")

	if got := r.Value("requests"); got != 3 {
		t.Errorf("Value(requests) = %d, want 3", got)
	}
}

func TestRegistry_Add(t *testing.T) {
	r := New()

	r.Add("bytes", 100)
	r.Add("bytes", 250)

	if got := r.Value("bytes"); got != 350 {
		t.Errorf("Value(bytes) = %d, want 350", got)
	}
}

func TestRegistry_Snapshot(t *testing.T) {
	r := New()
	r.Incr("a")
	r.Incr("b")
	r.Incr("b")

	snap := r.Snapshot()

	if snap["a"] != 1 {
		t.Errorf("snap[a] = %d, want 1", snap["a"])
	}
	if snap["b"] != 2 {
		t.Errorf("snap[b] = %d, want 2", snap["b"])
	}
	if len(snap) != 2 {
		t.Errorf("len(snap) = %d, want 2", len(snap))
	}
}

func TestRegistry_Format(t *testing.T) {
	r := New()
	r.Add("zz_last", 3)
	r.Add("aa_first", 1)
	r.Add("mm_middle", 2)

	got := r.Format()
	want := "aa_first=1\nmm_middle=2\nzz_last=3\n"

	if got != want {
		t.Errorf("Format() =\n%s\nwant:\n%s", got, want)
	}
}

func TestRegistry_EmptyFormat(t *testing.T) {
	r := New()
	if got := r.Format(); got != "" {
		t.Errorf("Format() = %q, want empty", got)
	}
}

func TestRegistry_Concurrent(t *testing.T) {
	r := New()
	var wg sync.WaitGroup

	const goroutines = 100
	const iterations = 1000

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				r.Incr("counter")
			}
		}()
	}
	wg.Wait()

	want := int64(goroutines * iterations)
	if got := r.Value("counter"); got != want {
		t.Errorf("concurrent Value(counter) = %d, want %d", got, want)
	}
}

func TestRegistry_MultipleCountersConcurrent(t *testing.T) {
	r := New()
	var wg sync.WaitGroup

	counters := []string{"search", "fetch", "llm", "cache"}
	const iterations = 500

	wg.Add(len(counters))
	for _, name := range counters {
		go func() {
			defer wg.Done()
			for range iterations {
				r.Incr(name)
			}
		}()
	}
	wg.Wait()

	for _, name := range counters {
		if got := r.Value(name); got != iterations {
			t.Errorf("Value(%s) = %d, want %d", name, got, iterations)
		}
	}

	snap := r.Snapshot()
	if len(snap) != len(counters) {
		t.Errorf("Snapshot has %d counters, want %d", len(snap), len(counters))
	}

	formatted := r.Format()
	for _, name := range counters {
		if !strings.Contains(formatted, name) {
			t.Errorf("Format() missing counter %q", name)
		}
	}
}
