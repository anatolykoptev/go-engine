package search

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/anatolykoptev/go-stealth/ratelimit"
)

// frozenClock returns a clock that always returns t; suitable for WithPacerClock
// so tests can inspect Allow/Wait behaviour without real sleeping.
func frozenClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// advanceable returns a clock whose time can be bumped by the caller.
func advanceable(start time.Time) (clock func() time.Time, advance func(time.Duration)) {
	cur := start
	clock = func() time.Time { return cur }
	advance = func(d time.Duration) { cur = cur.Add(d) }
	return clock, advance
}

// TestScraperPacer_FirstHitImmediate verifies that the first Allow for any key
// is never blocked: there is no prior grant to space against.
//
// Red-on-revert: removing the first-hit logic from KeyedPacer would cause the
// first Allow to be blocked by a zero-value nextAllowed entry, failing this test.
func TestScraperPacer_FirstHitImmediate(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := ratelimit.NewKeyedPacer(
		500*time.Millisecond, 0,
		ratelimit.WithPacerClock(frozenClock(base)),
	)

	if !p.Allow("engine-a") {
		t.Fatal("first Allow blocked; want immediate (no prior grant)")
	}
	if !p.Allow("engine-b") {
		t.Fatal("first Allow for independent key blocked; want immediate")
	}

	// Falsification: second hit to same key IS blocked within the min-delay window.
	// Without the first-hit logic this assertion would pass vacuously (both blocked).
	if p.Allow("engine-a") {
		t.Fatal("second Allow not blocked within window; falsification: pacer appears broken")
	}
}

// TestScraperPacer_RepeatHitSpaced verifies that a second request to the same
// key is blocked until minDelay has elapsed on the injected clock.
//
// Red-on-revert: setting minDelay to 0 makes the second Allow always return true,
// failing the "second Allow blocked" assertion.
func TestScraperPacer_RepeatHitSpaced(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock, advance := advanceable(base)
	const minDelay = 500 * time.Millisecond

	p := ratelimit.NewKeyedPacer(minDelay, 0, ratelimit.WithPacerClock(clock))

	if !p.Allow("engine-a") {
		t.Fatal("first Allow blocked; want immediate")
	}
	// Immediately after first grant: must be blocked.
	if p.Allow("engine-a") {
		t.Fatal("second Allow not blocked within minDelay window")
	}
	// Just before the window expires: still blocked.
	advance(minDelay - time.Millisecond)
	if p.Allow("engine-a") {
		t.Fatal("Allow unblocked before minDelay elapsed; want still blocked")
	}
	// One millisecond past the window: must be allowed.
	advance(2 * time.Millisecond) // total: minDelay+1ms
	if !p.Allow("engine-a") {
		t.Fatal("Allow still blocked after minDelay elapsed; want allowed")
	}
}

// TestScraperPacer_DifferentKeysNotSerialized verifies that distinct engine keys
// are always allowed concurrently: the pacer is per-key, not global.
//
// Red-on-revert: replacing per-key nextAllowed with a shared global lock would
// make the second key blocked after the first is granted, failing this test.
func TestScraperPacer_DifferentKeysNotSerialized(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := ratelimit.NewKeyedPacer(
		500*time.Millisecond, 0,
		ratelimit.WithPacerClock(frozenClock(base)),
	)

	keys := []string{"engine-a", "engine-b", "engine-c", "engine-d"}
	for _, k := range keys {
		if !p.Allow(k) {
			t.Fatalf("first Allow(%q) blocked; want immediate (independent per-key)", k)
		}
	}
	// All keys are now within their min-delay window — each must be individually blocked.
	for _, k := range keys {
		if p.Allow(k) {
			t.Fatalf("second Allow(%q) not blocked within window", k)
		}
	}
}

// TestSearchDirect_PacerFirstHitPassesThrough verifies that SearchDirect delivers
// results normally when a pacer is configured: the first hit per engine is always
// immediate, so a single-query fan-out incurs zero delay.
//
// Red-on-revert: wiring the pacer before the first hit in a way that blocks would
// cause this test to return 0 results (engine skipped or timed out).
func TestSearchDirect_PacerFirstHitPassesThrough(t *testing.T) {
	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return []byte(ddgHTML(2)), nil, http200, nil
	}}

	// Frozen clock at t=0: any second hit would block for 2 s, but the first is immediate.
	pacer := ratelimit.NewKeyedPacer(
		2*time.Second, 0,
		ratelimit.WithPacerClock(frozenClock(time.Now())),
	)

	cfg := DirectConfig{
		Browser: bc,
		DDG:     true,
		Pacer:   pacer,
	}
	results := SearchDirect(context.Background(), cfg, "test query", "en")
	if len(results) == 0 {
		t.Fatal("got no results; pacer must not block the first hit per engine")
	}
}

// TestSearchDirect_PacerCtxCancelSkipsEngine verifies that context cancellation
// while the pacer is holding a key causes the engine to be skipped gracefully:
// SearchDirect returns within the ctx deadline with no panic.
//
// Red-on-revert: removing the ctx.Err() check from the pacer wrapper would cause
// SearchDirect to block until the pacer's 10 s window expires (>>200 ms deadline).
func TestSearchDirect_PacerCtxCancelSkipsEngine(t *testing.T) {
	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return []byte(ddgHTML(2)), nil, http200, nil
	}}

	// Create a pacer blocked on the engine for 10 s (frozen clock, already primed).
	pacer := ratelimit.NewKeyedPacer(
		10*time.Second, 0,
		ratelimit.WithPacerClock(frozenClock(time.Now())),
	)
	// Prime "ddg" so the next call to that key is blocked for 10 s.
	pacer.Allow("ddg")

	cfg := DirectConfig{
		Browser: bc,
		DDG:     true,
		Pacer:   pacer,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_ = SearchDirect(ctx, cfg, "test query", "en")
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Fatalf("SearchDirect took %v; want <1s (ctx cancellation must abort pacer wait)", elapsed)
	}
}

// http200 is a named constant to avoid a magic-number lint warning in test helpers.
const http200 = 200
