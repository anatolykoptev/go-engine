package imagesearch

import (
	stealth "github.com/anatolykoptev/go-stealth"

	"github.com/anatolykoptev/go-engine/internal/chromehdr"
)

// searchHeadersFor returns the Chrome navigation headers for the client d
// presents (go-stealth's canonical set, see internal/chromehdr), without
// accept-encoding. d is the BrowserDoer the caller will issue the request
// through; nil or a test mock gets go-stealth's default profile.
//
// Go's http.Client handles decompression automatically when accept-encoding
// is not explicitly set; setting it manually disables auto-decompression
// and causes regex parsers to fail on compressed responses.
func searchHeadersFor(d any) map[string]string {
	return chromehdr.ForOptions(d, stealth.HeaderOptions{OmitAcceptEncoding: true})
}

// fetchHeadersFor returns the headers of a page-issued fetch() to the page's
// own origin (an XHR-style JSON call), without accept-encoding, for the client
// d presents.
func fetchHeadersFor(d any) map[string]string {
	return chromehdr.ForOptions(d, stealth.HeaderOptions{
		Mode:               stealth.FetchCORS,
		Site:               stealth.FetchSiteSameOrigin,
		OmitAcceptEncoding: true,
	})
}

// androidHeaders returns headers mimicking the Google Go Android app.
// This UA triggers Google to return ~50 JSON results instead of 10 HTML.
// Exact format from SearXNG searx/engines/google_images.py.
func androidHeaders() map[string]string {
	return map[string]string{
		"user-agent":      "NSTN/3.60.474802233.release Dalvik/2.1.0 (Linux; U; Android 12; US) gzip",
		"accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"accept-language": "en-US,en;q=0.9",
	}
}
