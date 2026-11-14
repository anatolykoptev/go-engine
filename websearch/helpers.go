package websearch

import (
	"bytes"
	"net/http"
	"regexp"
	"strings"

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
