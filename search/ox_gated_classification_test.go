package search

// ox_gated_classification_test.go — gated-SERP classification for the
// ox-browser escalation runners (go-search #317).
//
// Production counters showed ~48% of Bing escalations returning
// successful-but-empty: HTTP 200, parseable, zero results. That shape is
// ambiguous — a genuine empty SERP vs the engine serving its captcha/rate-
// limit variant to the escalated profile (escalation fires exactly when the
// engine is already hostile to our egress). The runners now run the
// engine's gate detector on zero-result pages and return outcome="captcha"
// so "empty" means a real SERP.
//
// Mutation: delete the IsBingRateLimited/isDDGRateLimited branch in
// runOxBing/runOxDDG → gated fixture parses to zero → outcome "empty"
// instead of "captcha" → RED.

import (
	"context"
	"sync/atomic"
	"testing"
)

// gatedBingHTML is a Bing gate page — no result markup, just the challenge.
const gatedBingHTML = `<html><body>
<h1>Our systems have detected unusual traffic from your computer network</h1>
<p>Please try your request again later.</p>
</body></html>`

// gatedDDGHTML is the DDG anomaly/captcha form page.
const gatedDDGHTML = `<html><body>
<form action="/d.js" method="post"><input type="hidden" name="q" value="x"></form>
<p>not a robot?</p>
</body></html>`

// gatedBraveHTML is a Brave verify-human page.
const gatedBraveHTML = `<html><body>
<h1>Please verify you are human</h1>
<div>Solve the challenge to continue.</div>
</body></html>`

// realEmptyBingHTML is a valid Bing SERP shell with zero result items —
// the shape a genuinely-empty query returns (b_results present, no b_algo).
const realEmptyBingHTML = `<html><body>
<ol id="b_results"><li class="b_no"><h2>There are no results for this query</h2></li></ol>
</body></html>`

func TestRunOxBing_GatedPage_OutcomeCaptcha(t *testing.T) {
	var fetches atomic.Int32
	cfg := DirectConfig{OxBrowserFetch: oxFetchFn(gatedBingHTML, &fetches)}
	res, outcome := runOxBing(context.Background(), cfg, "test query")
	if outcome != "captcha" {
		t.Fatalf("gated Bing page must classify as captcha, got %q", outcome)
	}
	if len(res) != 0 {
		t.Fatalf("captcha outcome must carry no results, got %d", len(res))
	}
}

func TestRunOxBing_RealEmptySERP_OutcomeEmpty(t *testing.T) {
	var fetches atomic.Int32
	cfg := DirectConfig{OxBrowserFetch: oxFetchFn(realEmptyBingHTML, &fetches)}
	_, outcome := runOxBing(context.Background(), cfg, "test query")
	if outcome != "empty" {
		t.Fatalf("real zero-result SERP must stay outcome=empty, got %q", outcome)
	}
}

func TestRunOxDDG_GatedPage_OutcomeCaptcha(t *testing.T) {
	var fetches atomic.Int32
	cfg := DirectConfig{OxBrowserFetch: oxFetchFn(gatedDDGHTML, &fetches)}
	_, outcome := runOxDDG(context.Background(), cfg, "test query")
	if outcome != "captcha" {
		t.Fatalf("gated DDG page must classify as captcha, got %q", outcome)
	}
}

func TestRunOxBrave_GatedPage_OutcomeCaptcha(t *testing.T) {
	var fetches atomic.Int32
	cfg := DirectConfig{OxBrowserFetch: oxFetchFn(gatedBraveHTML, &fetches)}
	_, outcome := runOxBrave(context.Background(), cfg, "test query")
	if outcome != "captcha" {
		t.Fatalf("gated Brave page must classify as captcha, got %q", outcome)
	}
}
