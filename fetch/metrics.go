package fetch

import (
	"github.com/prometheus/client_golang/prometheus"
)

// fetchMetrics is the internal interface used by fetchDirectFirst to record
// observability events. The default implementation (promTierMetrics) writes to
// Prometheus. Tests inject a lightweight in-process stub via withMetrics.
//
// Keeping the interface internal means consumers do not depend on prometheus
// unless they wire up the default metrics themselves via InitMetrics.
type fetchMetrics interface {
	// incTier records a completed fetch for the given tier and result.
	// tier: "direct" | "proxy"
	// result: "ok" | "err"
	incTier(tier, result string)

	// incBlockSignal records a block signal detected on direct tier.
	// signal: "hard" | "soft" | "tls"
	incBlockSignal(signal string)

	// incEscalation records a direct→proxy escalation and its reason.
	// reason: "hard" | "soft" | "tls" | "domain_hint" | "cached"
	incEscalation(reason string)

	// setBlockCacheHosts updates the current size of the direct-block FIFO cache.
	setBlockCacheHosts(n int)

	// incSSRFBlocked records a fetch refused by the destination guard (blocked
	// address, redirect hop, DNS rebind, or disallowed scheme). Name-resolution
	// failures are counted by incResolveRefused instead.
	incSSRFBlocked()

	// incResolveRefused records a fetch refused, fail closed, because the
	// destination name did not resolve (after one retry of the lookup). Not an
	// attack signal: dead and flaky hosts land here.
	incResolveRefused()
}

// noopMetrics is a no-op implementation — used when WithMetrics is not called.
type noopMetrics struct{}

func (noopMetrics) incTier(_, _ string)      {}
func (noopMetrics) incBlockSignal(_ string)  {}
func (noopMetrics) incEscalation(_ string)   {}
func (noopMetrics) setBlockCacheHosts(_ int) {}
func (noopMetrics) incSSRFBlocked()          {}
func (noopMetrics) incResolveRefused()       {}

// promTierMetrics implements fetchMetrics using prometheus counter/gauge vectors.
type promTierMetrics struct {
	tierTotal      *prometheus.CounterVec
	blockSignal    *prometheus.CounterVec
	escalations    *prometheus.CounterVec
	blockCacheSize prometheus.Gauge
	ssrfBlocked    prometheus.Counter
	resolveRefused prometheus.Counter
}

// NewPromMetrics creates a promTierMetrics registered on the given prometheus.Registerer.
// Pass prometheus.DefaultRegisterer for production use.
//
// Metric names follow the go_engine_fetch_ prefix:
//
//	go_engine_fetch_tier_total{tier, result}
//	go_engine_fetch_block_signal_total{signal}
//	go_engine_fetch_proxy_escalations_total{reason}
//	go_engine_fetch_direct_block_cache_hosts
//	go_engine_fetch_ssrf_blocked_total
//	go_engine_fetch_resolve_refused_total
func NewPromMetrics(reg prometheus.Registerer) (fetchMetrics, error) {
	tierTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "go_engine_fetch_tier_total",
		Help: "Number of fetches per tier (direct|proxy) and result (ok|err).",
	}, []string{"tier", "result"})

	blockSignal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "go_engine_fetch_block_signal_total",
		Help: "Block signals detected on the direct tier, classified by type (hard|soft|tls).",
	}, []string{"signal"})

	escalations := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "go_engine_fetch_proxy_escalations_total",
		Help: "Direct→proxy escalations by reason (hard|soft|tls|domain_hint|cached).",
	}, []string{"reason"})

	blockCacheSize := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "go_engine_fetch_direct_block_cache_hosts",
		Help: "Current number of hosts in the direct-block FIFO cache.",
	})

	ssrfBlocked := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "go_engine_fetch_ssrf_blocked_total",
		Help: "Fetches refused by the destination guard (blocked address, redirect hop, DNS rebind, disallowed scheme). Names that fail to resolve are counted separately.",
	})

	resolveRefused := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "go_engine_fetch_resolve_refused_total",
		Help: "Fetches refused, fail closed, because the destination name did not resolve after one retry of the lookup. Not an attack signal.",
	})

	for _, c := range []prometheus.Collector{tierTotal, blockSignal, escalations, blockCacheSize, ssrfBlocked, resolveRefused} {
		if err := reg.Register(c); err != nil {
			return nil, err
		}
	}

	return &promTierMetrics{
		tierTotal:      tierTotal,
		blockSignal:    blockSignal,
		escalations:    escalations,
		blockCacheSize: blockCacheSize,
		ssrfBlocked:    ssrfBlocked,
		resolveRefused: resolveRefused,
	}, nil
}

func (m *promTierMetrics) incTier(tier, result string) {
	m.tierTotal.WithLabelValues(tier, result).Inc()
}

func (m *promTierMetrics) incBlockSignal(signal string) {
	m.blockSignal.WithLabelValues(signal).Inc()
}

func (m *promTierMetrics) incEscalation(reason string) {
	m.escalations.WithLabelValues(reason).Inc()
}

func (m *promTierMetrics) setBlockCacheHosts(n int) {
	m.blockCacheSize.Set(float64(n))
}

func (m *promTierMetrics) incSSRFBlocked() {
	m.ssrfBlocked.Inc()
}

func (m *promTierMetrics) incResolveRefused() {
	m.resolveRefused.Inc()
}
