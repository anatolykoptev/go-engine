package websearch

import (
	"bytes"
	"net/http"
	"regexp"
	"strings"

	stealth "github.com/anatolykoptev/go-stealth"

	"github.com/anatolykoptev/go-engine/internal/chromehdr"
)

var reHTMLTag = regexp.MustCompile(`<[^>]*>`)

// Common Accept / Content-Type header values reused across search providers.
const (
	acceptJSON           = "application/json"
	acceptHTML           = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
	acceptFormURLEncoded = "application/x-www-form-urlencoded"
)

// CleanHTML strips HTML tags and trims whitespace.
func CleanHTML(s string) string {
	return strings.TrimSpace(reHTMLTag.ReplaceAllString(s, ""))
}

// ChromeHeadersFor returns the Chrome navigation headers for the browser d
// presents: go-stealth's canonical set for that client's identity (see
// internal/chromehdr). d is the BrowserDoer the caller will issue the request
// through; nil or a test mock gets go-stealth's default profile.
func ChromeHeadersFor(d any) map[string]string {
	return chromehdr.For(d)
}

// sec-fetch-site values the engines use, named so the call sites read as the
// relationship they describe.
const (
	siteSameOrigin = stealth.FetchSiteSameOrigin // Referer is the target's own origin
	siteSameSite   = stealth.FetchSiteSameSite   // Referer is another origin of the same site
)

// ChromeHeadersForSite is ChromeHeadersFor for a navigation whose Referer is
// on site relative to the target: use stealth.FetchSiteSameOrigin when the
// Referer is the target's own origin (a link or form on the engine's page).
// Real Chrome never sends sec-fetch-site: none together with a Referer.
func ChromeHeadersForSite(d any, site stealth.FetchSite) map[string]string {
	return chromehdr.ForOptions(d, stealth.HeaderOptions{Site: site})
}

// ChromeFetchHeadersFor returns the headers of a page-issued fetch() or XHR to
// a JSON endpoint, for the browser d presents: sec-fetch-mode cors, dest
// empty, no document-only headers. site is where the request goes relative to
// the page that issues it (same-origin, same-site, cross-site).
func ChromeFetchHeadersFor(d any, site stealth.FetchSite) map[string]string {
	return chromehdr.ForOptions(d, stealth.HeaderOptions{Mode: stealth.FetchCORS, Site: site})
}

// ChromeHeaders returns the Chrome navigation headers for the default profile.
//
// Deprecated: use ChromeHeadersFor(d) with the request's BrowserDoer so the
// headers describe the client's own identity.
func ChromeHeaders() map[string]string {
	return ChromeHeadersFor(nil)
}

// IsDDGRateLimited checks whether the DDG response body indicates CAPTCHA.
func IsDDGRateLimited(body []byte) bool {
	low := bytes.ToLower(body)
	for _, marker := range [][]byte{
		[]byte("please try again"),
		[]byte("not a robot"),
		[]byte("unusual traffic"),
		[]byte("blocked"),
	} {
		if bytes.Contains(low, marker) {
			return true
		}
	}
	return bytes.Contains(low, []byte(`action="/d.js"`)) &&
		bytes.Contains(low, []byte(`type="hidden"`))
}

// isStartpageRateLimited checks if Startpage blocked the request.
func isStartpageRateLimited(body []byte) bool {
	lower := bytes.ToLower(body)
	markers := [][]byte{
		[]byte("rate limited"),
		[]byte("too many requests"),
		[]byte("g-recaptcha"),
		[]byte("captcha"),
	}
	for _, m := range markers {
		if bytes.Contains(lower, m) {
			return true
		}
	}
	return false
}

// isRateLimitStatus returns true for HTTP status codes that indicate rate limiting.
func isRateLimitStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusForbidden
}
