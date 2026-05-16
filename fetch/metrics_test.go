package fetch

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// atomicMetrics is a test-local fetchMetrics stub backed by atomic counters.
// It avoids any global prometheus registry, making tests isolation-safe.
type atomicMetrics struct {
	tierDirect    atomic.Int64
	tierDirectErr atomic.Int64
	tierProxy     atomic.Int64
	tierProxyErr  atomic.Int64
	signalHard    atomic.Int64
	signalSoft    atomic.Int64
	signalTLS     atomic.Int64
	escalHard     atomic.Int64
	escalSoft     atomic.Int64
	escalTLS      atomic.Int64
	escalHint     atomic.Int64
	escalCached   atomic.Int64
	cacheHosts    atomic.Int64
}

func (m *atomicMetrics) incTier(tier, result string) {
	switch tier + "/" + result {
	case "direct/ok":
		m.tierDirect.Add(1)
	case "direct/err":
		m.tierDirectErr.Add(1)
	case "proxy/ok":
		m.tierProxy.Add(1)
	case "proxy/err":
		m.tierProxyErr.Add(1)
	}
}

func (m *atomicMetrics) incBlockSignal(signal string) {
	switch signal {
	case "hard":
		m.signalHard.Add(1)
	case "soft":
		m.signalSoft.Add(1)
	case "tls":
		m.signalTLS.Add(1)
	}
}

func (m *atomicMetrics) incEscalation(reason string) {
	switch reason {
	case "hard":
		m.escalHard.Add(1)
	case "soft":
		m.escalSoft.Add(1)
	case "tls":
		m.escalTLS.Add(1)
	case "domain_hint":
		m.escalHint.Add(1)
	case "cached":
		m.escalCached.Add(1)
	}
}

func (m *atomicMetrics) setBlockCacheHosts(n int) {
	m.cacheHosts.Store(int64(n))
}

// newMetricsFetcher builds a directFirst Fetcher with atomicMetrics injected.
func newMetricsFetcher(m fetchMetrics) *Fetcher {
	f := newDirectFirstBase()
	f.metrics = m
	return f
}

// Test 1: Direct OK → tier{direct,ok} increments; no escalation or signal.
func Test_Metrics_DirectOK(t *testing.T) {
	m := &atomicMetrics{}
	f := newMetricsFetcher(m)
	f.directClient = &fakeDoer{
		status: http.StatusOK,
		body:   largeHTML("hello"),
		hdrs:   map[string]string{"content-type": "text/html"},
	}

	if _, err := f.FetchBody(context.Background(), "https://example.com/page"); err != nil {
		t.Fatalf("FetchBody: %v", err)
	}

	if got := m.tierDirect.Load(); got != 1 {
		t.Errorf("tier{direct,ok} = %d, want 1", got)
	}
	if got := m.tierProxy.Load(); got != 0 {
		t.Errorf("tier{proxy,ok} = %d, want 0 (no proxy needed)", got)
	}
	if got := m.signalHard.Load() + m.signalSoft.Load() + m.signalTLS.Load(); got != 0 {
		t.Errorf("block signals = %d, want 0", got)
	}
	if got := m.escalHard.Load() + m.escalSoft.Load() + m.escalTLS.Load(); got != 0 {
		t.Errorf("escalations = %d, want 0", got)
	}
}

// Test 2: Direct 403 → signal{hard}, escalation{hard}, tier{proxy,ok}.
func Test_Metrics_403_EscalatesHard(t *testing.T) {
	m := &atomicMetrics{}
	f := newMetricsFetcher(m)
	f.directClient = &fakeDoer{
		status: http.StatusForbidden,
		body:   []byte("blocked"),
		hdrs:   map[string]string{},
	}
	f.proxyClient = &fakeDoer{
		status: http.StatusOK,
		body:   largeHTML("proxy response"),
		hdrs:   map[string]string{"content-type": "text/html"},
	}

	if _, err := f.FetchBody(context.Background(), "https://blocked.example.com/page"); err != nil {
		t.Fatalf("FetchBody: %v", err)
	}

	if got := m.signalHard.Load(); got != 1 {
		t.Errorf("signal{hard} = %d, want 1", got)
	}
	if got := m.escalHard.Load(); got != 1 {
		t.Errorf("escalation{hard} = %d, want 1", got)
	}
	if got := m.tierProxy.Load(); got != 1 {
		t.Errorf("tier{proxy,ok} = %d, want 1", got)
	}
	if got := m.tierDirect.Load(); got != 0 {
		t.Errorf("tier{direct,ok} = %d, want 0 (direct blocked, not ok)", got)
	}
}

// Test 3: Domain hint skip → escalation{domain_hint}, tier{proxy,ok}; direct never called.
func Test_Metrics_DomainHint(t *testing.T) {
	m := &atomicMetrics{}
	f := newMetricsFetcher(m)
	// Direct client should never be called — use a doer that fails if invoked.
	f.directClient = &fakeDoer{err: http.ErrServerClosed}
	f.proxyClient = &fakeDoer{
		status: http.StatusOK,
		body:   largeHTML("linkedin response"),
		hdrs:   map[string]string{"content-type": "text/html"},
	}

	if _, err := f.FetchBody(context.Background(), "https://www.linkedin.com/jobs/view/123"); err != nil {
		t.Fatalf("FetchBody: %v", err)
	}

	if got := m.escalHint.Load(); got != 1 {
		t.Errorf("escalation{domain_hint} = %d, want 1", got)
	}
	if got := m.tierProxy.Load(); got != 1 {
		t.Errorf("tier{proxy,ok} = %d, want 1", got)
	}
	// direct must not have contributed any tier counter.
	if got := m.tierDirect.Load() + m.tierDirectErr.Load(); got != 0 {
		t.Errorf("direct tier counters = %d, want 0 (domain hint skips direct)", got)
	}
}

// Test 4: Block-cache hit on 2nd call → escalation{cached} on 2nd call.
func Test_Metrics_BlockCacheHit(t *testing.T) {
	m := &atomicMetrics{}
	f := newMetricsFetcher(m)
	f.directClient = &fakeDoer{
		status: http.StatusForbidden,
		body:   []byte("blocked"),
		hdrs:   map[string]string{},
	}
	f.proxyClient = &fakeDoer{
		status: http.StatusOK,
		body:   largeHTML("ok"),
		hdrs:   map[string]string{"content-type": "text/html"},
	}

	const host = "sticky.example.com"
	// First call: 403 → escalation{hard}, marks blockCache.
	if _, err := f.FetchBody(context.Background(), "https://"+host+"/one"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Second call: blockCache hit → escalation{cached}.
	if _, err := f.FetchBody(context.Background(), "https://"+host+"/two"); err != nil {
		t.Fatalf("second call: %v", err)
	}

	if got := m.escalCached.Load(); got != 1 {
		t.Errorf("escalation{cached} = %d, want 1 (second call should hit cache)", got)
	}
	if got := m.escalHard.Load(); got != 1 {
		t.Errorf("escalation{hard} = %d, want 1 (only first call escalates via signal)", got)
	}
}

// Test 5: DirectBlockCache.Len() tracks gauge correctly after Mark and eviction.
func Test_BlockCache_Len(t *testing.T) {
	c := NewDirectBlockCache(10*time.Minute, 3) // cap=3 to force eviction

	if got := c.Len(); got != 0 {
		t.Errorf("Len() = %d, want 0 (empty)", got)
	}

	c.Mark("a.com")
	if got := c.Len(); got != 1 {
		t.Errorf("Len() = %d, want 1 after Mark(a.com)", got)
	}

	c.Mark("b.com")
	c.Mark("c.com")
	if got := c.Len(); got != 3 {
		t.Errorf("Len() = %d, want 3", got)
	}

	// cap=3; adding a 4th triggers eviction of oldest (a.com).
	c.Mark("d.com")
	if got := c.Len(); got != 3 {
		t.Errorf("Len() = %d, want 3 after eviction (cap=3)", got)
	}
	if c.IsBlocked("a.com") {
		t.Errorf("a.com should have been evicted")
	}
	if !c.IsBlocked("d.com") {
		t.Errorf("d.com should be in cache")
	}
}

// Test 6: NewPromMetrics registers correctly on a local registry (no global state).
func Test_NewPromMetrics_Register(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := NewPromMetrics(reg)
	if err != nil {
		t.Fatalf("NewPromMetrics: %v", err)
	}

	// Drive some counts through the interface.
	m.incTier("direct", "ok")
	m.incTier("direct", "ok")
	m.incTier("proxy", "ok")
	m.incBlockSignal("hard")
	m.incEscalation("hard")
	m.setBlockCacheHosts(5)

	// Gather from the local registry and verify families are present.
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	want := map[string]bool{
		"go_engine_fetch_tier_total":                 false,
		"go_engine_fetch_block_signal_total":         false,
		"go_engine_fetch_proxy_escalations_total":    false,
		"go_engine_fetch_direct_block_cache_hosts":   false,
	}
	for _, mf := range mfs {
		if _, ok := want[mf.GetName()]; ok {
			want[mf.GetName()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("metric %q not found in gathered output", name)
		}
	}

	// Double-register must fail (protect against accidental re-init in prod).
	if _, err2 := NewPromMetrics(reg); err2 == nil {
		t.Error("second NewPromMetrics on same registry should return error, got nil")
	}
}
