// Package chromehdr resolves the Chrome request headers a go-engine request
// carries. go-stealth owns the header set (identity User-Agent, Client Hints,
// Accept, Accept-Encoding, Sec-Fetch-*, Upgrade-Insecure-Requests, Priority),
// tested against real Chrome captures; this package only picks which identity
// describes the doer a request goes through.
package chromehdr

import stealth "github.com/anatolykoptev/go-stealth"

// Source is a doer that can describe the browser it presents: the concrete
// *stealth.BrowserClient, and any wrapper that forwards to one (the search
// package's primary/fallback doer does).
type Source interface {
	RequestHeaders(opts stealth.HeaderOptions) (stealth.RequestHeaders, error)
}

// For returns the Chrome navigation headers for the browser d presents. A doer
// that does not describe itself (nil, a test mock) gets the same set for
// go-stealth's default identity. d is the doer the caller will issue the
// request through.
func For(d any) map[string]string {
	return ForOptions(d, stealth.HeaderOptions{})
}

// ForOptions is For with go-stealth's header options: a fetch() instead of a
// navigation, a sec-fetch-site value, a locale, no accept-encoding.
//
// go-stealth refuses an identity that is not Chromium and an unknown
// sec-fetch-site; go-engine builds neither, so such an error falls back to the
// default identity rather than returning an empty set.
func ForOptions(d any, opts stealth.HeaderOptions) map[string]string {
	if rh, err := request(d, opts); err == nil {
		return rh.Values
	}
	rh, err := stealth.DefaultIdentity().RequestHeaders(opts)
	if err != nil {
		return map[string]string{"user-agent": stealth.DefaultIdentity().UserAgent}
	}
	return rh.Values
}

func request(d any, opts stealth.HeaderOptions) (stealth.RequestHeaders, error) {
	switch v := d.(type) {
	case *stealth.BrowserClient:
		if v != nil {
			return v.RequestHeaders(opts)
		}
	case Source:
		return v.RequestHeaders(opts)
	}
	return stealth.DefaultIdentity().RequestHeaders(opts)
}
