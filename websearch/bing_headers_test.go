package websearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestBing_RequestCarriesRealChromeNavigationHeaders compares the headers a
// Bing request carries with what real Google Chrome 156 sent for a top-level
// navigation (../internal/chromehdr/testdata/chrome156_nav_request.json, a
// subset of go-stealth's testdata/wire/google_chrome_156_nav.json).
//
// Why it matters: on a datacenter egress Bing answers natural-language
// queries from one template word of the query ("parts", "explain", "does")
// far more often when the request lacks Client Hints, and go-engine's own
// four-header builder sent none. A new session, 20 unseen queries per header
// set: the old set 15 collapsed, the go-stealth set 4.
//
// User-Agent and the platform hint depend on the operating system of the
// client's profile, and Accept is the one value Bing deliberately overrides
// (see Bing.Search), so those are not compared.
//
// Falsification: replace `return chromehdr.For(d)` (websearch/helpers.go:31)
// with the four-header literal ChromeHeadersFor returned before it delegated to
// go-stealth (accept, accept-language, accept-encoding, user-agent) and this
// test goes RED (sec-ch-ua, sec-fetch-*, ... missing).
func TestBing_RequestCarriesRealChromeNavigationHeaders(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../internal/chromehdr/testdata/chrome156_nav_request.json")
	if err != nil {
		t.Fatalf("read reference: %v", err)
	}
	var ref struct {
		Headers map[string]string `json:"request_headers"`
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatalf("decode reference: %v", err)
	}

	var got map[string]string
	bc := &mockBrowser{fn: func(_, _ string, h map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		got = h
		return []byte(`<html><body><ol id="b_results"></ol></body></html>`), nil, http.StatusOK, nil
	}}
	if _, err := NewBing(WithBingBrowser(bc)).Search(context.Background(), "what are the parts of a merkle tree", SearchOpts{}); err != nil {
		t.Fatalf("Search: %v", err)
	}

	for name, want := range ref.Headers {
		have, ok := got[name]
		if !ok {
			t.Errorf("header %q missing, real Chrome sends %q", name, want)
			continue
		}
		switch name {
		case "user-agent", "sec-ch-ua-platform":
			continue
		case "accept":
			want = acceptHTML
		}
		if have != want {
			t.Errorf("header %q = %q, want %q", name, have, want)
		}
	}
	if !strings.Contains(got["user-agent"], "Chrome/156.") {
		t.Errorf("user-agent %q is not the default Chrome 156 identity", got["user-agent"])
	}
}

// TestParseBingHTML_FullSERPFixture parses a SERP in the full layout (reduced
// from a real capture: titles, snippets and destination URLs are verbatim; the
// Bing click wrapper keeps only its u= parameter; scripts, styles, tracking
// parameters and every non-result module are removed, the b_ans and b_pag
// items are stubs). The headers above move every Bing request from the lite
// layout to this one, so the parse of its result items is pinned here.
//
// Falsification: comment out `href = bingUnwrapURL(href)` in parseBingResult
// (websearch/bing.go:146) and this test goes RED (results[0].URL is the Bing
// click wrapper).
func TestParseBingHTML_FullSERPFixture(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("testdata/bing_serp_full.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	results, err := ParseBingHTML(data)
	if err != nil {
		t.Fatalf("ParseBingHTML: %v", err)
	}
	if len(results) != 10 {
		t.Fatalf("got %d results, want 10", len(results))
	}
	if want := "https://cassandra.apache.org/doc/latest/cassandra/managing/operating/compaction/tombstones.html"; results[0].URL != want {
		t.Errorf("results[0].URL = %q, want %q", results[0].URL, want)
	}
	if !strings.Contains(results[0].Title, "Tombstones") {
		t.Errorf("results[0].Title = %q, want it to contain Tombstones", results[0].Title)
	}
	for i, r := range results {
		if strings.Contains(r.URL, "bing.com/ck/a") {
			t.Errorf("results[%d].URL %q still wrapped", i, r.URL)
		}
	}
}
