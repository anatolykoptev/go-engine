package fetch

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// testPublicIP is the documentation-free public address the test network answers
// every DNS lookup with, so URLs naming a public-looking host pass the guard.
const testPublicIP = "93.184.216.34"

// testNetworkDialTimeout bounds the connect to the test address.
const testNetworkDialTimeout = 5 * time.Second

// WithTestNetwork is for tests that fetch from a local fixture server. It keeps
// the destination guard fully active and changes only what the network answers:
//
//   - every DNS lookup the guard makes is answered with a public address, so a
//     URL naming a public-looking host (for example http://fixture.test/page)
//     passes the pre-request and per-redirect-hop checks;
//   - the plain-HTTP tier connects to addr (host:port) whatever host the URL
//     names.
//
// A URL that is a literal loopback, private or metadata address is still refused,
// and the Chrome-TLS tiers are not affected. Because the dialed address is chosen
// by the code that passes addr, never by a URL, this cannot be used to steer a
// fetch at an internal address.
//
// It takes a testing.TB so that it can only be called from a test: production
// code has no TB to pass.
func WithTestNetwork(tb testing.TB, addr string) Option {
	tb.Helper()
	return func(f *Fetcher) {
		f.guard.resolver = publicResolver{}
		f.guard.transport = fixedAddrTransport{addr: addr}
	}
}

// publicResolver answers every lookup with testPublicIP.
type publicResolver struct{}

func (publicResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return []net.IPAddr{{IP: net.ParseIP(testPublicIP)}}, nil
}

// fixedAddrTransport dials addr for every request. It is a struct wrapper, not a
// bare *http.Transport, because go-kit re-guards (and so replaces the dialer of)
// a bare *http.Transport; any other RoundTripper stays as the inner leg under
// the guard's per-hop checks.
type fixedAddrTransport struct{ addr string }

func (t fixedAddrTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	d := net.Dialer{Timeout: testNetworkDialTimeout}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return d.DialContext(ctx, network, t.addr)
		},
		DisableKeepAlives: true,
	}
	return tr.RoundTrip(r)
}
