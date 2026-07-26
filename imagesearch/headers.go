package imagesearch

import stealth "github.com/anatolykoptev/go-stealth"

// searchHeadersFor returns Chrome-like headers without accept-encoding, with
// the User-Agent derived from d's identity so it agrees with the JA3
// fingerprint the presenting stealth client sends. d is the BrowserDoer the
// caller will issue the request through; pass nil to resolve from the default
// profile (ProfileChrome131).
//
// Go's http.Client handles decompression automatically when accept-encoding
// is not explicitly set; setting it manually disables auto-decompression
// and causes regex parsers to fail on compressed responses.
func searchHeadersFor(d any) map[string]string {
	h := stealth.ChromeHeaders()
	if bc, ok := d.(*stealth.BrowserClient); ok && bc != nil {
		h["user-agent"] = bc.Identity().UserAgent
	} else {
		h["user-agent"] = stealth.UserAgentForProfile(stealth.ProfileChrome131)
	}
	delete(h, "accept-encoding")
	return h
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
