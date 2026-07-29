//nolint:goconst
package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/anatolykoptev/go-engine/metrics"
	"github.com/anatolykoptev/go-engine/sources"
	"github.com/anatolykoptev/go-engine/websearch"
)

const (
	metricMarginaliaRequests = "marginalia_requests"
	marginaliaDirectScore    = 1.0

	// defaultMarginaliaDailyBudget is the default per-calendar-day query cap
	// enforced by MarginaliaBudget when NewMarginaliaBudget is called with a
	// non-positive limit. The Marginalia maintainer granted a personal
	// non-commercial key on an explicit expectation of 50–100 queries/day; 80
	// sits in the middle of that range, leaving headroom on both ends. The
	// fleet's other direct sources run ~1400 dispatches/day each, so wiring
	// Marginalia in without a budget would exceed the granted quota by >10×.
	defaultMarginaliaDailyBudget = 80

	// metricMarginaliaBudgetRemaining is the gauge exposing the remaining
	// Marginalia queries for the current UTC calendar day. Follows the
	// go_search_ prefix convention of the sibling fan-out metrics so it
	// groups with them in go-search dashboards. Exhaustion is observable as
	// this gauge reaching 0, rather than inferred from an absence of results.
	metricMarginaliaBudgetRemaining = "go_search_marginalia_budget_remaining"

	// marginaliaSourceURL is the link-back the operator promised the
	// Marginalia maintainer in exchange for the personal key. Surfaced in
	// every result's Metadata so a consumer can credit + link the source.
	marginaliaSourceURL = "https://search.marginalia.nu/"
)

// ErrMarginaliaQuotaExhausted is returned by runMarginalia when the daily
// Marginalia budget is spent. It is a deliberate shed outcome, NOT an engine
// failure: callers can errors.Is it to distinguish "out of courtesy quota"
// from a genuine transport/parse error. It is returned promptly (non-blocking)
// and the outbound HTTP request is never issued.
var ErrMarginaliaQuotaExhausted = errors.New("marginalia: daily quota exhausted")

// marginaliaResp is the decoded shape of the Marginalia Nu public search API
// response. The top-level License field (CC-BY-NC-SA 4.0) is surfaced into
// each result's Metadata so consumers can credit + link the source as the
// maintainer's usage terms require.
type marginaliaResp struct {
	License string `json:"license"`
	Results []struct {
		URL         string  `json:"url"`
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Quality     float64 `json:"quality"`
	} `json:"results"`
}

// SearchMarginaliaDirect queries the Marginalia Nu search API.
// key is the API key path segment (the maintainer grants a personal
// non-commercial key); an empty key defaults to "public", the shared
// heavily-rate-limited demo key, preserving prior behaviour. The key is
// url.PathEscape'd exactly like the query so a key with an unexpected
// character cannot produce a malformed URL or silently hit a different path.
// Returns nil, nil on 429 (rate-limited) so callers degrade gracefully.
//
// The key is never logged and never included in a returned error: a *url.Error
// from the underlying transport would carry the full URL (and thus the key) in
// its message, so any bc.Do error is re-wrapped with the URL stripped.
func SearchMarginaliaDirect(ctx context.Context, bc BrowserDoer, query, key string, m *metrics.Registry) ([]sources.Result, error) {
	if m != nil {
		m.Incr(metricMarginaliaRequests)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if key == "" {
		key = "public"
	}
	apiURL := "https://api.marginalia.nu/" + url.PathEscape(key) + "/search/" + url.PathEscape(query) + "?count=10"

	headers := websearch.ChromeHeadersFor(bc)
	headers["accept"] = "application/json"

	data, _, status, err := bc.Do(http.MethodGet, apiURL, headers, nil)
	if err != nil {
		// Strip the URL from any *url.Error in the chain: the URL path segment
		// carries the API key, which must not reach a log or error message.
		// The underlying error is preserved (via %w on the unwrapped Err) so
		// errors.Is(err, context.Canceled/DeadlineExceeded) still holds for
		// upstream classification.
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("marginalia: request failed: %w", ue.Err)
		}
		return nil, fmt.Errorf("marginalia: request failed: %w", err)
	}
	if status == http.StatusTooManyRequests {
		slog.Warn("marginalia: rate limited (429), skipping")
		return nil, nil
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("marginalia: unexpected status %d", status)
	}

	return ParseMarginaliaJSON(data)
}

// ParseMarginaliaJSON decodes Marginalia public API JSON into sources.Result slice.
// Exported for unit tests.
func ParseMarginaliaJSON(data []byte) ([]sources.Result, error) {
	var resp marginaliaResp
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("marginalia: json decode: %w", err)
	}

	results := make([]sources.Result, 0, len(resp.Results))
	for _, item := range resp.Results {
		if item.URL == "" || item.Title == "" {
			continue
		}
		md := map[string]string{
			"engine":     "marginalia",
			"source_url": marginaliaSourceURL,
			"license":    resp.License,
		}
		results = append(results, sources.Result{
			Title:    item.Title,
			URL:      item.URL,
			Content:  item.Description,
			Score:    marginaliaDirectScore,
			Metadata: md,
		})
	}
	return results, nil
}

// MarginaliaBudget is a self-imposed per-calendar-day query cap for the
// Marginalia source. The maintainer granted a personal non-commercial key on
// an expectation of 50–100 queries/day; the fleet's other direct sources run
// ~1400 dispatches/day each, so without a cap Marginalia would blow past the
// granted quota by >10× and the key would be withdrawn.
//
// Window: per UTC calendar day (reset at 00:00 UTC). UTC is chosen over a
// host-local timezone because the search service runs on a server where UTC
// is deterministic and host-independent, and a calendar-day boundary (vs a
// rolling 24h window) prevents a burst at a local-TZ midnight from consuming
// 2× the budget across the boundary.
//
// Restart behaviour: the counter is in-memory and resets to the full limit on
// every process restart. Worst-case daily spend = limit × (restarts+1). With
// the default limit of 80 and a bad day of N restarts, that is 80×(N+1)
// queries — still far below the other sources' ~1400/day but above the 100
// promised; the operator controls deploy frequency and may lower the limit.
// The counter is NOT persisted: persisting would require a store dependency
// (Redis via go-kit/ratelimit.SlidingWindow) that this source does not
// otherwise carry, which is disproportionate for a courtesy quota.
//
// Acquire is non-blocking: when the budget is exhausted it returns false
// promptly so the source sheds load rather than queueing. The remaining budget
// is published to the metricMarginaliaBudgetRemaining gauge so exhaustion is
// observable (gauge → 0) rather than inferred from an absence of results.
//
// A nil *MarginaliaBudget in DirectConfig falls back to a package-level
// default instance (limit = defaultMarginaliaDailyBudget, no metrics registry)
// so the quota is enforced even when the consumer has not wired one; wiring
// NewMarginaliaBudget with the consumer's *metrics.Registry additionally
// publishes the remaining-budget gauge.
type MarginaliaBudget struct {
	limit int
	now   func() time.Time
	m     *metrics.Registry

	mu    sync.Mutex
	day   string // "2006-01-02" UTC of the current window
	spent int    // calls issued in the current window
}

// NewMarginaliaBudget returns a MarginaliaBudget with the given per-day limit.
// A non-positive limit defaults to defaultMarginaliaDailyBudget. When m is
// non-nil the remaining-budget gauge is published.
func NewMarginaliaBudget(limit int, m *metrics.Registry) *MarginaliaBudget {
	return newMarginaliaBudget(limit, m, time.Now)
}

// newMarginaliaBudget is the testable constructor with an injectable clock.
func newMarginaliaBudget(limit int, m *metrics.Registry, now func() time.Time) *MarginaliaBudget {
	if limit <= 0 {
		limit = defaultMarginaliaDailyBudget
	}
	b := &MarginaliaBudget{limit: limit, now: now, m: m}
	b.reset(now())
	return b
}

// reset starts a fresh window for the given time's UTC calendar day.
func (b *MarginaliaBudget) reset(t time.Time) {
	b.day = t.UTC().Format("2006-01-02")
	b.spent = 0
	b.publishRemaining()
}

// Acquire records one query against the daily budget. It returns true if the
// query is permitted (and decrements remaining), or false if the budget is
// exhausted — non-blocking, never waits. On UTC calendar-day rollover the
// counter resets to the full limit before evaluating the call.
func (b *MarginaliaBudget) Acquire() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	today := b.now().UTC().Format("2006-01-02")
	if today != b.day {
		b.day = today
		b.spent = 0
	}
	if b.spent >= b.limit {
		b.publishRemaining()
		return false
	}
	b.spent++
	b.publishRemaining()
	return true
}

// publishRemaining sets the gauge to limit-spent. Called under b.mu.
func (b *MarginaliaBudget) publishRemaining() {
	if b.m == nil {
		return
	}
	b.m.Gauge(metricMarginaliaBudgetRemaining).Set(float64(b.limit - b.spent))
}

// defaultMarginaliaBudget is the package-level fallback used when a
// DirectConfig enables Marginalia without wiring a *MarginaliaBudget. It
// enforces the default limit so the courtesy quota is protected even under
// operator misconfiguration, but it has no metrics registry and so does not
// publish the remaining-budget gauge (wire NewMarginaliaBudget with the
// consumer's registry for observability).
var defaultMarginaliaBudget = newMarginaliaBudget(defaultMarginaliaDailyBudget, nil, time.Now)
