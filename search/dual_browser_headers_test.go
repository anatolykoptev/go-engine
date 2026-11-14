package search

import (
	"strings"
	"testing"

	stealth "github.com/anatolykoptev/go-stealth"

	"github.com/anatolykoptev/go-engine/websearch"
)

// TestDualBrowser_HeadersDescribePrimary pins that headers built from the
// primary/fallback wrapper (which is what every scraper in a SearchDirect
// fan-out gets when a fallback doer is configured) carry the identity of the
// primary client, not a default one. A client on a non-default platform makes
// the difference visible: its User-Agent and platform hint must come through.
//
// Falsification: delete the ChromeHeaders method of dualBrowser
// (search/dual_browser.go) and this test goes RED (the wrapper is no longer a
// header source, so the scrapers get the default Windows identity).
func TestDualBrowser_HeadersDescribePrimary(t *testing.T) {
	t.Parallel()
	var mac stealth.BrowserProfile
	for _, p := range stealth.BuiltinProfiles {
		if p.TLSProfile == stealth.ProfileChrome156 && strings.Contains(p.UserAgent, "Macintosh") {
			mac = p
			break
		}
	}
	if mac.UserAgent == "" {
		t.Fatal("no macOS Chrome 156 entry in stealth.BuiltinProfiles")
	}
	primary, err := stealth.NewClient(stealth.WithIdentity(stealth.BrowserIdentity{BrowserProfile: mac}))
	if err != nil {
		t.Fatalf("primary: %v", err)
	}
	fallback, err := stealth.NewClient()
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}

	h := websearch.ChromeHeadersFor(newDualBrowser(primary, fallback))
	if h["user-agent"] != mac.UserAgent {
		t.Errorf("user-agent = %q, want the primary's %q", h["user-agent"], mac.UserAgent)
	}
	if h["sec-ch-ua-platform"] != `"macOS"` {
		t.Errorf("sec-ch-ua-platform = %q, want \"macOS\"", h["sec-ch-ua-platform"])
	}
}
