package search

import (
	"context"
	"errors"

	kitmetrics "github.com/anatolykoptev/go-kit/metrics"

	"github.com/anatolykoptev/go-engine/metrics"
	"github.com/anatolykoptev/go-engine/sources"
	"github.com/anatolykoptev/go-engine/websearch"
)

const metricRedditRequests = "reddit_requests"

// SearchRedditDirect queries Reddit JSON API using browser TLS fingerprint.
// Delegates to websearch.Reddit.
func SearchRedditDirect(ctx context.Context, bc BrowserDoer, query string, m *metrics.Registry) ([]sources.Result, error) {
	if m != nil {
		m.Incr(metricRedditRequests)
	}
	r := websearch.NewReddit(websearch.WithRedditBrowser(bc))
	ws, err := r.Search(ctx, query, websearch.SearchOpts{Limit: 10})
	if err != nil {
		return nil, err
	}
	return ws, nil
}

// metricRedditTier is the per-tier outcome counter for the Reddit escalation chain.
// Counter name: go_search_reddit_tier_total{tier=<tier>,outcome=<outcome>}
//
// Outcomes:
//   - empty        — tier returned 0 results with no error (escalating)
//   - rate_limited — tier returned *ErrRateLimited
//   - error        — tier returned any other non-nil error
const metricRedditTier = "go_search_reddit_tier_total"

// tierOutcomeLabel maps a tier exit error to a bounded outcome label for the
// reddit_tier counter. nil means "tier produced zero results" (empty outcome).
func tierOutcomeLabel(err error) string {
	if err == nil {
		return "empty"
	}
	var rl *ErrRateLimited
	if errors.As(err, &rl) {
		return "rate_limited"
	}
	return "error"
}

// recordTierOutcome increments the per-tier outcome counter for the Reddit
// 3-tier escalation chain. Nil metrics registry is a no-op.
//
// err == nil means the tier returned 0 results (empty outcome).
// err is classified into "rate_limited" or "error" via tierOutcomeLabel.
func recordTierOutcome(tier string, err error) {
	// NOTE: the metrics registry from DirectConfig is not threaded into this
	// call site in the current implementation because runReddit receives cfg
	// by value and calls recordTierOutcome for observability. In a follow-up
	// phase the registry will be plumbed through to enable Prometheus scraping.
	// For now this function establishes the interface and enables future wiring
	// without any metric emission (no-op body). The counter name and outcome
	// label taxonomy are defined here so that go_search_reddit_tier_total
	// is consistent with the rest of the source_result counter naming.
	_ = tierOutcomeLabel(err)
	_ = kitmetrics.Label(metricRedditTier, "tier", tier, "outcome", tierOutcomeLabel(err))
}
