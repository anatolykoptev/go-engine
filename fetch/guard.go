package fetch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"

	kithttputil "github.com/anatolykoptev/go-kit/httputil"
	stealth "github.com/anatolykoptev/go-stealth"
)

// The destination guard.
//
// Every URL a [Fetcher] is given is a caller-supplied destination, so every
// client that can dial one carries the same three-tier go-kit policy, in every
// mode (direct-first on or off, proxy or no proxy):
//
//  1. connect-time dial control on the already-resolved address (rebind-proof);
//  2. a redirect guard on every hop;
//  3. a pre-request URL check.
//
// The policy is built in exactly one place, [guard], and each client is
// derived from it: [guard.httpClient] for the standard-library tier and
// [guard.stealthOptions] for both Chrome-TLS tiers. [guard.check] additionally
// runs once at the top of FetchBody so that the forwarding fallbacks
// (ox-browser, go-browser, Byparr), which hand the URL to another service to
// dial, never receive a blocked destination.
//
// The proxy tier dials an operator-configured proxy, so its connect-time check
// sees the proxy address, not the destination; the URL and redirect checks are
// what guard the destination there. The connect-time check still matters on that
// client: a pool that runs dry falls back to dialing the destination directly.

// guard is the destination policy shared by every client the Fetcher builds.
// The zero value is the production policy; the fields exist only so tests can
// replace DNS answers and the network leg, never to switch the policy off.
type guard struct {
	// resolver answers the guard's DNS lookups (pre-request check and connect
	// time). nil selects the system resolver.
	resolver kithttputil.Resolver
	// transport replaces the plain tier's network leg. nil selects a real
	// dial-guarded transport.
	transport http.RoundTripper
}

// errAllowed is returned by allowTransport to tell guard.check that the
// guarded round tripper accepted the URL.
var errAllowed = errors.New("fetch: url allowed")

// allowTransport is the inner transport behind the check-only client: it never
// dials, it only reports that the guard let the request through.
type allowTransport struct{}

func (allowTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, errAllowed }

func (g guard) ssrfOptions() []kithttputil.SSRFOption {
	if g.resolver == nil {
		return nil
	}
	return []kithttputil.SSRFOption{kithttputil.WithResolver(g.resolver)}
}

// check is the pre-request URL guard. It runs the same go-kit policy as the
// guarded round tripper (scheme allowlist, alternate-IP encodings, every
// resolved address) through the same resolver the clients use.
func (g guard) check(ctx context.Context, u *neturl.URL) error {
	if g.resolver == nil {
		return kithttputil.CheckURL(ctx, u)
	}
	if u == nil {
		return fmt.Errorf("%w: nil URL", kithttputil.ErrSSRFBlocked)
	}
	probe := kithttputil.NewSSRFGuardedClient(&http.Client{Transport: allowTransport{}}, g.ssrfOptions()...)
	req := (&http.Request{Method: http.MethodGet, URL: u, Header: http.Header{}}).WithContext(ctx)
	resp, err := probe.Transport.RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close() // allowTransport never returns one; keeps the contract honest
	}
	if errors.Is(err, errAllowed) {
		return nil
	}
	return err
}

// checkRaw parses rawURL and applies check. A URL that does not parse is
// refused: no tier could dial it anyway, and a forwarder must not be handed it.
func (g guard) checkRaw(ctx context.Context, rawURL string) error {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: parse %q: %w", kithttputil.ErrSSRFBlocked, rawURL, err)
	}
	return g.check(ctx, u)
}

// httpClient derives the plain-HTTP tier client from base. The dial control,
// per-hop check (every redirect re-enters the guarded transport) and
// pre-request check come from go-kit; base's timeout and redirect cap carry over.
func (g guard) httpClient(base *http.Client) *http.Client {
	cc := *base
	if g.transport != nil {
		cc.Transport = g.transport
	}
	return kithttputil.NewSSRFGuardedClient(&cc, g.ssrfOptions()...)
}

// stealthOptions returns the three guard tiers for a go-stealth client. They
// override go-stealth's own stdlib floor with go-kit's policy and are shared by
// the direct client and the proxy client, so neither can be built without them.
func (g guard) stealthOptions() []stealth.ClientOption {
	redirect, dial := kithttputil.SSRFGuards()
	return []stealth.ClientOption{
		stealth.WithDialControl(dial),
		stealth.WithRedirectGuard(redirect),
		stealth.WithRequestURLGuard(g.check),
	}
}

// ErrSSRFBlocked matches (errors.Is) every refusal by the destination guard,
// whichever tier raised it: the pre-request check, a redirect hop, or the
// connect-time check. It is the go-kit sentinel, so callers that already match
// the go-kit one keep working.
//
// A refusal is terminal: no tier retries it and no fallback (another tier, a
// forwarder, the plain HTTP client) is attempted for the same URL.
var ErrSSRFBlocked = kithttputil.ErrSSRFBlocked

// blockedError carries a guard refusal across the retry layer. It deliberately
// has no Unwrap: the retry classifier looks for *net.OpError / *net.DNSError
// with errors.As and would otherwise treat a refused dial as a transient network
// failure worth retrying. errors.Is still sees the original chain through Is.
type blockedError struct {
	cause   error
	resolve bool // the refusal was a failed name lookup, not a blocked address
}

func (e *blockedError) Error() string { return e.cause.Error() }

func (e *blockedError) Is(target error) bool {
	return target == ErrSSRFBlocked || errors.Is(e.cause, target)
}

// terminalIfBlocked turns a guard refusal into a non-retryable error that still
// matches ErrSSRFBlocked, and returns every other error unchanged.
func terminalIfBlocked(err error) error {
	if err == nil || !isSSRFBlocked(err) {
		return err
	}
	var already *blockedError
	if errors.As(err, &already) {
		return err
	}
	return &blockedError{cause: err, resolve: isResolveFailure(err)}
}

// isResolveFailure reports whether a guard refusal is a name that did not
// resolve rather than an address the policy blocks. go-kit fails closed on a
// lookup error (the host is refused, never dialed), but an unresolvable host is
// routine for a web fetcher and must not read as an attack in the counter.
func isResolveFailure(err error) bool {
	var b *blockedError
	if errors.As(err, &b) {
		return b.resolve
	}
	var d *net.DNSError
	return errors.As(err, &d)
}

// isSSRFBlocked reports whether err is a destination-guard refusal from either
// the go-kit policy or go-stealth's own guard sentinel.
func isSSRFBlocked(err error) bool {
	return errors.Is(err, kithttputil.ErrSSRFBlocked) || errors.Is(err, stealth.ErrSSRFBlocked)
}
