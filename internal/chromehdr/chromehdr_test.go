package chromehdr

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	stealth "github.com/anatolykoptev/go-stealth"
)

// reference is a real Google Chrome 156 (stable, branded, macOS) top-level
// navigation as a header-echo service saw it (testdata/chrome156_nav_request.json,
// a subset of go-stealth's testdata/wire/google_chrome_156_nav.json).
type reference struct {
	Names   []string          `json:"request_header_names"`
	Headers map[string]string `json:"request_headers"`
}

func loadReference(t *testing.T) reference {
	t.Helper()
	raw, err := os.ReadFile("testdata/chrome156_nav_request.json")
	if err != nil {
		t.Fatalf("read reference: %v", err)
	}
	var ref reference
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatalf("decode reference: %v", err)
	}
	return ref
}

// macChrome156 is the BuiltinProfiles entry whose User-Agent is the one the
// reference browser sent.
func macChrome156(t *testing.T, ua string) stealth.BrowserProfile {
	t.Helper()
	for _, p := range stealth.BuiltinProfiles {
		if p.UserAgent == ua {
			return p
		}
	}
	t.Fatalf("no BuiltinProfiles entry with the reference User-Agent %q", ua)
	return stealth.BrowserProfile{}
}

// TestFor_ClientMatchesRealChrome156 compares the headers go-engine builds for
// a client with the macOS Chrome 156 identity against what real Chrome 156
// sent for a top-level navigation: the same header names and the same value
// for every one of them. The wire order is the client's (go-stealth's own
// oracle covers it); a map has none.
//
// Falsification: make For (internal/chromehdr/chromehdr.go) return the
// pre-delegation four-header literal (accept, accept-language, accept-encoding,
// user-agent) and this test goes RED (missing sec-ch-ua, sec-fetch-*, ...).
func TestFor_ClientMatchesRealChrome156(t *testing.T) {
	t.Parallel()
	ref := loadReference(t)
	p := macChrome156(t, ref.Headers["user-agent"])
	c, err := stealth.NewClient(stealth.WithIdentity(stealth.BrowserIdentity{BrowserProfile: p}))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	got := For(c)
	assertSameHeaders(t, got, ref)
}

func assertSameHeaders(t *testing.T, got map[string]string, ref reference) {
	t.Helper()
	for _, name := range ref.Names {
		want := ref.Headers[name]
		have, ok := got[name]
		if !ok {
			// The reference records a header Chrome sends on every navigation
			// that the doer did not supply. Connection-level headers (none are
			// in the reference) would be exempt; nothing here is.
			t.Errorf("header %q missing, real Chrome sends %q", name, want)
			continue
		}
		if have != want {
			t.Errorf("header %q = %q, real Chrome sends %q", name, have, want)
		}
	}
	names := make(map[string]bool, len(ref.Names))
	for _, n := range ref.Names {
		names[n] = true
	}
	for k := range got {
		if !names[k] {
			t.Errorf("header %q is not one real Chrome sends on a navigation", k)
		}
	}
}

// TestFor_DefaultProfileForUnknownDoer pins what a doer that cannot describe
// itself (nil, a test mock, a typed-nil client) gets: the default profile's
// headers, not a half set and not a panic. The values that depend on the
// profile's operating system (User-Agent, platform hint) are not compared with
// the macOS reference; everything else is.
func TestFor_DefaultProfileForUnknownDoer(t *testing.T) {
	t.Parallel()
	ref := loadReference(t)
	var typedNil *stealth.BrowserClient
	for name, d := range map[string]any{"nil": nil, "mock": struct{}{}, "typed nil client": typedNil} {
		got := For(d)
		for _, k := range ref.Names {
			if _, ok := got[k]; !ok {
				t.Errorf("%s: header %q missing", name, k)
			}
			if k == "user-agent" || k == "sec-ch-ua-platform" {
				continue
			}
			if got[k] != ref.Headers[k] {
				t.Errorf("%s: header %q = %q, real Chrome sends %q", name, k, got[k], ref.Headers[k])
			}
		}
		if !strings.Contains(got["user-agent"], "Chrome/156.") {
			t.Errorf("%s: user-agent %q is not the default Chrome 156 identity", name, got["user-agent"])
		}
	}
}

type fakeSource map[string]string

func (f fakeSource) RequestHeaders(stealth.HeaderOptions) (stealth.RequestHeaders, error) {
	return stealth.RequestHeaders{Values: f}, nil
}

// TestFor_UsesSourceBehindWrapper pins that a wrapper which forwards to a
// stealth client (the search package's primary/fallback doer) is asked for its
// identity instead of silently getting the default one.
//
// Falsification: delete the `case Source:` branch in For
// (internal/chromehdr/chromehdr.go) and this test goes RED.
func TestFor_UsesSourceBehindWrapper(t *testing.T) {
	t.Parallel()
	got := For(fakeSource{"user-agent": "wrapped"})
	if got["user-agent"] != "wrapped" {
		t.Fatalf("user-agent = %q, want the wrapped source's", got["user-agent"])
	}
}

// TestForOptions_NonChromiumClientKeepsItsOwnUserAgent pins the fallback for a
// client whose identity is not Chromium: go-stealth has no Chrome header set
// for it, and the default Chrome identity would contradict its TLS handshake,
// so it gets its own User-Agent and no Chrome Client Hints or Sec-Fetch set.
//
// Falsification: delete the `errors.Is(err, stealth.ErrNotChromium)` branch in
// fallback (internal/chromehdr/chromehdr.go) and this test goes RED (the
// client is described as the default Chrome, with sec-ch-ua).
func TestForOptions_NonChromiumClientKeepsItsOwnUserAgent(t *testing.T) {
	t.Parallel()
	c, err := stealth.NewClient(stealth.WithProfile(stealth.ProfileFirefox148))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	got := ForOptions(c, stealth.HeaderOptions{})
	if got["user-agent"] != c.Identity().UserAgent || !strings.Contains(got["user-agent"], "Firefox") {
		t.Errorf("user-agent = %q, want the Firefox client's own %q", got["user-agent"], c.Identity().UserAgent)
	}
	for k := range got {
		if strings.HasPrefix(k, "sec-") || k == "upgrade-insecure-requests" {
			t.Errorf("header %q sent for a non-Chromium client", k)
		}
	}
}
