package websearch

import (
	"regexp"
	"testing"

	stealth "github.com/anatolykoptev/go-stealth"
)

var chromeMajorRE = regexp.MustCompile(`Chrome/(\d+)`)

// chromeMajor extracts the Chrome major version token from a User-Agent
// string (e.g. "Chrome/131.0.0.0" -> "131"). Fatals if absent — every UA in
// this invariant's domain is Chromium-based by construction (the fleet's
// stealth clients all use ProfileChrome131, a Chrome TLS profile).
func chromeMajor(t *testing.T, ua string) string {
	t.Helper()
	m := chromeMajorRE.FindStringSubmatch(ua)
	if m == nil {
		t.Fatalf("no Chrome major version token in UA %q", ua)
	}
	return m[1]
}

// TestChromeHeadersFor_MatchesDefaultProfileChromeMajor pins the invariant
// that the User-Agent websearch sends has the same Chrome major as the TLS
// profile the fleet's stealth client presents. go-stealth pairs a UA with a
// JA3 fingerprint as a matched pair in BuiltinProfiles; a hardcoded UA
// literal riding a different profile's fingerprint is a self-inconsistent
// pair — no real Chrome 120 produces a Chrome 131 handshake — which is a
// stronger bot signal than being merely out of date.
//
// The former chromeUserAgents pool rotated through Chrome 131/130, Safari
// 17.2, and Firefox 115 UAs over a Chrome 131 JA3; 5 of 8 entries violated
// this invariant. This test fails the moment the UA drifts from the
// presenting profile's Chrome major.
//
// Both resolution paths are covered: the nil fallback (resolveUserAgent
// resolves from the default profile) and the identity path (a
// default-constructed *stealth.BrowserClient, mirroring the fleet's
// no-WithProfile construction in fetch/fetcher.go).
func TestChromeHeadersFor_MatchesDefaultProfileChromeMajor(t *testing.T) {
	t.Parallel()
	// The default profile every stealth.NewClient() in this repo installs
	// when no WithProfile is given (stealth defaultConfig: ProfileChrome131).
	profileUA := stealth.UserAgentForProfile(stealth.ProfileChrome131)
	wantMajor := chromeMajor(t, profileUA)

	// Nil fallback: no *stealth.BrowserClient in hand (tests, ad-hoc).
	nilHeaders := ChromeHeadersFor(nil)
	if got := chromeMajor(t, nilHeaders["user-agent"]); got != wantMajor {
		t.Fatalf("nil-fallback UA Chrome major %q != default profile Chrome major %q (ua=%q profile=%q)",
			got, wantMajor, nilHeaders["user-agent"], profileUA)
	}

	// Identity path: a default-constructed BrowserClient (no WithProfile →
	// ProfileChrome131, exactly as fetch/fetcher.go:200/215 builds it).
	bc, err := stealth.NewClient()
	if err != nil {
		t.Fatalf("stealth.NewClient() error: %v", err)
	}
	idHeaders := ChromeHeadersFor(bc)
	if got := chromeMajor(t, idHeaders["user-agent"]); got != wantMajor {
		t.Fatalf("identity UA Chrome major %q != default profile Chrome major %q (ua=%q profile=%q)",
			got, wantMajor, idHeaders["user-agent"], profileUA)
	}

	// The identity path must agree with Identity().UserAgent exactly — not
	// just the Chrome major, the full string.
	if idHeaders["user-agent"] != bc.Identity().UserAgent {
		t.Fatalf("identity UA %q != bc.Identity().UserAgent %q",
			idHeaders["user-agent"], bc.Identity().UserAgent)
	}
}
