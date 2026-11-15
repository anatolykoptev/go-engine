// Package chromehdr resolves the Chrome request headers a go-engine request
// carries. go-stealth owns the header set (identity User-Agent, Client Hints,
// Accept, Accept-Encoding, Sec-Fetch-*, Upgrade-Insecure-Requests, Priority),
// tested against real Chrome captures; this package only picks which identity
// describes the doer a request goes through.
package chromehdr

import (
	"errors"
	"log/slog"
	"sync"

	stealth "github.com/anatolykoptev/go-stealth"
)

// Source is a doer that can describe the browser it presents: the concrete
// *stealth.BrowserClient, and any wrapper that forwards to one (the search
// package's primary/fallback doer does).
type Source interface {
	RequestHeaders(opts stealth.HeaderOptions) (stealth.RequestHeaders, error)
}

// identified is a doer that can name its identity, for the fallback when the
// identity is not Chromium and has no Chrome header set.
type identified interface {
	Identity() stealth.BrowserIdentity
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
// go-stealth refuses an identity that is not Chromium (stealth.ErrNotChromium)
// and an unknown sec-fetch-site. A non-Chromium client gets its own
// User-Agent and nothing else: Firefox and Safari send no Chrome Client Hints
// or Sec-Fetch set of this shape, and the default Chrome identity would
// contradict its TLS handshake. The fallback is logged, once per process.
func ForOptions(d any, opts stealth.HeaderOptions) map[string]string {
	rh, err := request(d, opts)
	if err == nil {
		return rh.Values
	}
	return fallback(d, opts, err)
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

// userAgent is the request header name go-stealth and this package key on.
const userAgent = "user-agent"

var warnOnce sync.Once

func fallback(d any, opts stealth.HeaderOptions, err error) map[string]string {
	warnOnce.Do(func() {
		slog.Warn("chromehdr: no Chrome header set for this client, using a reduced one", slog.Any("error", err))
	})
	if errors.Is(err, stealth.ErrNotChromium) {
		if c, ok := d.(*stealth.BrowserClient); ok && c != nil {
			return map[string]string{userAgent: c.Identity().UserAgent}
		}
		if id, ok := d.(identified); ok {
			return map[string]string{userAgent: id.Identity().UserAgent}
		}
	}
	// An invalid option (go-engine passes none) or a doer that cannot name its
	// identity: the default identity with default options still describes a
	// browser the default TLS profile can be.
	if rh, derr := stealth.DefaultIdentity().RequestHeaders(stealth.HeaderOptions{Mode: opts.Mode}); derr == nil {
		return rh.Values
	}
	return map[string]string{userAgent: stealth.DefaultIdentity().UserAgent}
}
