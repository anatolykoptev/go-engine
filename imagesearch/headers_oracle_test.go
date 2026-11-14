package imagesearch

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	stealth "github.com/anatolykoptev/go-stealth"
)

type refHeaders struct {
	Headers map[string]string `json:"request_headers"`
}

func loadRef(t *testing.T, path string) refHeaders {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read reference: %v", err)
	}
	var ref refHeaders
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatalf("decode reference: %v", err)
	}
	return ref
}

func macClient(t *testing.T, ua string) *stealth.BrowserClient {
	t.Helper()
	for _, p := range stealth.BuiltinProfiles {
		if p.UserAgent == ua {
			c, err := stealth.NewClient(stealth.WithIdentity(stealth.BrowserIdentity{BrowserProfile: p}))
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			return c
		}
	}
	t.Fatalf("no BuiltinProfiles entry with the reference User-Agent %q", ua)
	return nil
}

// TestFetchHeadersFor_MatchRealChromeFetch compares the headers of the DDG
// image JSON call with what real Google Chrome 156 sent for a page fetch() to
// its own origin (testdata/chrome156_fetch_request.json, a subset of
// go-stealth's testdata/wire/google_chrome_156_fetch.json): same names, same
// values, except accept-encoding, which these callers leave out so the body
// arrives undecoded-by-hand.
//
// Falsification: make fetchHeadersFor (imagesearch/headers.go) use the default
// navigation mode (drop `Mode: stealth.FetchCORS`) and this test goes RED
// (accept, sec-fetch-mode, sec-fetch-dest and priority differ from the fetch).
func TestFetchHeadersFor_MatchRealChromeFetch(t *testing.T) {
	t.Parallel()
	ref := loadRef(t, "testdata/chrome156_fetch_request.json")
	got := fetchHeadersFor(macClient(t, ref.Headers["user-agent"]))
	for name, want := range ref.Headers {
		if name == "accept-encoding" {
			if _, ok := got[name]; ok {
				t.Errorf("accept-encoding present: %q", got[name])
			}
			continue
		}
		if got[name] != want {
			t.Errorf("header %q = %q, real Chrome sends %q", name, got[name], want)
		}
	}
	for k := range got {
		if _, ok := ref.Headers[k]; !ok {
			t.Errorf("header %q is not one real Chrome sends on a fetch()", k)
		}
	}
}

// TestSearchHeadersFor_NavigationWithoutAcceptEncoding pins the navigation
// variant: a navigation (not a fetch) with accept-encoding left out.
//
// Falsification: drop `OmitAcceptEncoding: true` in searchHeadersFor
// (imagesearch/headers.go) and this test goes RED.
func TestSearchHeadersFor_NavigationWithoutAcceptEncoding(t *testing.T) {
	t.Parallel()
	got := searchHeadersFor(nil)
	if _, ok := got["accept-encoding"]; ok {
		t.Errorf("accept-encoding present: %q", got["accept-encoding"])
	}
	if got["sec-fetch-mode"] != "navigate" || got["sec-fetch-dest"] != "document" {
		t.Errorf("not a navigation: mode=%q dest=%q", got["sec-fetch-mode"], got["sec-fetch-dest"])
	}
	if !strings.Contains(got["user-agent"], "Chrome/156.") || got["sec-ch-ua"] == "" {
		t.Errorf("default identity missing: ua=%q sec-ch-ua=%q", got["user-agent"], got["sec-ch-ua"])
	}
}
