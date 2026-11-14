package websearch

import (
	"context"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
)

// chromeMajorInUA is the Chrome major token of the User-Agent a request sends.
var chromeMajorInUA = regexp.MustCompile(`Chrome/(\d+)`)

// TestBing_SendsClientHintsMatchingUA pins the request shape that keeps Bing
// from degrading natural-language queries.
//
// Observed on a datacenter egress (go-search, 2026-11-14): a Chrome
// User-Agent without Sec-CH-UA hints got a ~120 KB lite SERP whose results
// all matched one template word of the query (42 of 48 unseen
// "what are the parts of X" / "explain X" / "what happens during X" queries
// came back as dictionary, car-parts and grammar pages); the same request
// plus the three client-hint headers got the full SERP with topical results
// (48 of 48). mkt, setlang, cc, form, Accept-Language and the query encoding
// left the degradation in place.
//
// Falsification: delete the addBingClientHints call in Bing.Search
// (websearch/bing.go) and this test goes RED with "sec-ch-ua header missing".
func TestBing_SendsClientHintsMatchingUA(t *testing.T) {
	t.Parallel()
	var got map[string]string
	bc := &mockBrowser{fn: func(_, _ string, h map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		got = h
		return []byte(`<html><body><ol id="b_results"></ol></body></html>`), nil, http.StatusOK, nil
	}}

	if _, err := NewBing(WithBingBrowser(bc)).Search(context.Background(), "what are the parts of a merkle tree", SearchOpts{}); err != nil {
		t.Fatalf("Search: %v", err)
	}

	ch := got["sec-ch-ua"]
	if ch == "" {
		t.Fatal("sec-ch-ua header missing")
	}
	if got["sec-ch-ua-mobile"] != "?0" {
		t.Errorf("sec-ch-ua-mobile = %q, want ?0", got["sec-ch-ua-mobile"])
	}
	if !strings.Contains(got["sec-ch-ua-platform"], `"`) || len(got["sec-ch-ua-platform"]) < 3 {
		t.Errorf("sec-ch-ua-platform = %q, want a quoted platform", got["sec-ch-ua-platform"])
	}

	// The hints must agree with the User-Agent that rides the same request.
	m := chromeMajorInUA.FindStringSubmatch(got["user-agent"])
	if m == nil {
		t.Fatalf("no Chrome major in user-agent %q", got["user-agent"])
	}
	if !strings.Contains(ch, `"Chromium";v="`+m[1]+`"`) {
		t.Errorf("sec-ch-ua %q does not carry Chromium major %s from user-agent %q", ch, m[1], got["user-agent"])
	}
}

// TestParseBingHTML_FullSERPFixture parses a real full-layout SERP (reduced
// from a capture: titles, snippets and destination URLs are verbatim, the
// Bing click wrapper keeps only its u= parameter). The client-hints change
// moves every Bing request from the lite layout to this one, so its
// structure (ten b_algo results, then a b_ans and a b_pag item that must not
// parse as results) is pinned here.
//
// Falsification: comment out `href = bingUnwrapURL(href)` in parseBingResult
// (websearch/bing.go:156) and this test goes RED (results[0].URL is the Bing
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
