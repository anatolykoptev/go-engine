package fetch

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Test fixtures for the destination guard. They never switch it off: URLs still
// go through the real policy, and only DNS answers and the plain tier's network
// leg are replaced (see WithTestNetwork).

// fixtureHost is the public-looking name fixture URLs use.
const fixtureHost = "fixture.test"

// fixturePublicIP is the address the fixture resolvers answer for any name.
const fixturePublicIP = testPublicIP

// fixtureURL returns a public-looking URL that WithTestNetwork routes to srv.
func fixtureURL(_ *httptest.Server) string { return "http://" + fixtureHost }

// countingTransport counts the requests that reach the network leg.
type countingTransport struct {
	hits atomic.Int32
	next http.RoundTripper
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.hits.Add(1)
	return c.next.RoundTrip(r)
}

// dialTo returns a transport that connects to srv's listener regardless of the
// host:port in the request. It is not a bare *http.Transport, for the reason
// given on fixedAddrTransport.
func dialTo(srv *httptest.Server) http.RoundTripper {
	return fixedAddrTransport{addr: srv.Listener.Addr().String()}
}

// withFixtureResolver answers DNS with a public address and leaves the real
// transport in place (for tests whose tiers are mock doers and never dial).
func withFixtureResolver() Option {
	return func(f *Fetcher) { f.guard.resolver = publicResolver{} }
}

// withFixtureNetwork routes the plain tier to srv through WithTestNetwork.
func withFixtureNetwork(tb testing.TB, srv *httptest.Server) Option {
	return WithTestNetwork(tb, srv.Listener.Addr().String())
}
