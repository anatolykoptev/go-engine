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
	"github.com/anatolykoptev/go-stealth/proxypool"
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
// what guard the destination there. A pool that returns no proxy would make
// go-stealth dial the destination directly through a dialer that carries no
// connect-time hook, so the pool is wrapped ([guardedPool]) and such requests go
// to a guarded direct client instead.

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
	// dialer is the base dialer of the plain tier; its Control hook runs before
	// the guard's. nil selects go-kit's default dialer.
	dialer *net.Dialer
}

// errAllowed is returned by allowTransport to tell guard.check that the
// guarded round tripper accepted the URL.
var errAllowed = errors.New("fetch: url allowed")

// allowTransport is the inner transport behind the check-only client: it never
// dials, it only reports that the guard let the request through.
type allowTransport struct{}

func (allowTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, errAllowed }

func (g guard) ssrfOptions() []kithttputil.SSRFOption {
	var opts []kithttputil.SSRFOption
	if g.resolver != nil {
		opts = append(opts, kithttputil.WithResolver(g.resolver))
	}
	if g.dialer != nil {
		opts = append(opts, kithttputil.WithDialer(g.dialer))
	}
	return opts
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

// ErrSSRFBlocked matches (errors.Is) every refusal of a blocked destination,
// whichever tier raised it: the pre-request check, a redirect hop, or the
// connect-time check. It is the go-kit sentinel, so callers that already match
// the go-kit one keep working.
//
// A refusal is terminal: no tier retries it and no fallback (another tier, a
// forwarder, the plain HTTP client) is attempted for the same URL.
var ErrSSRFBlocked = kithttputil.ErrSSRFBlocked

// ErrResolveFailed matches (errors.Is) a destination that was refused because its
// name could not be resolved (a DNS failure, or the caller's context ending
// during the lookup). The guard fails closed, so the host is not dialed and not
// forwarded, but nothing was found to be blocked: callers must not report this
// as an attack. The underlying cause (*net.DNSError, context.DeadlineExceeded,
// context.Canceled) stays reachable through errors.Is and errors.As.
var ErrResolveFailed = errors.New("fetch: destination name did not resolve")

// resolveError is a guard refusal caused by a failed name lookup. It wraps only
// the lookup's cause, never go-kit's ErrSSRFBlocked.
type resolveError struct {
	host  string
	cause error
}

func (e *resolveError) Error() string {
	if e.host == "" {
		return "fetch: name resolution failed, destination not fetched: " + e.cause.Error()
	}
	return "fetch: name resolution failed for " + e.host + ", destination not fetched: " + e.cause.Error()
}

func (e *resolveError) Unwrap() error { return e.cause }

func (e *resolveError) Is(target error) bool { return target == ErrResolveFailed }

// resolveCause extracts the lookup failure from a guard error: the *net.DNSError
// if there is one, otherwise the context error. It returns nil when err is not a
// lookup failure.
func resolveCause(err error) error {
	var d *net.DNSError
	switch {
	case errors.As(err, &d):
		return d
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.Is(err, context.Canceled):
		return context.Canceled
	}
	return nil
}

// isResolveFailure reports whether a guard refusal is a failed name lookup
// rather than an address the policy blocks.
func isResolveFailure(err error) bool {
	var r *resolveError
	return errors.As(err, &r) || (isSSRFBlocked(err) && resolveCause(err) != nil)
}

// asResolveError converts a guard error that is a failed lookup into a
// resolveError for host (host may be empty).
func asResolveError(err error, host string) error {
	var r *resolveError
	if errors.As(err, &r) {
		return err
	}
	cause := resolveCause(err)
	if cause == nil {
		return err
	}
	var d *net.DNSError
	if host == "" && errors.As(err, &d) {
		host = d.Name
	}
	return &resolveError{host: host, cause: cause}
}

// blockedError carries a guard refusal across the retry layer. It deliberately
// has no Unwrap: the retry classifier looks for *net.OpError / *net.DNSError
// with errors.As and would otherwise treat a refused dial as a transient network
// failure worth retrying. errors.Is still sees the original chain through Is.
type blockedError struct{ cause error }

func (e *blockedError) Error() string { return e.cause.Error() }

func (e *blockedError) Is(target error) bool {
	return target == ErrSSRFBlocked || errors.Is(e.cause, target)
}

// terminalIfBlocked turns a guard refusal into a non-retryable error that still
// matches ErrSSRFBlocked, and returns every other error unchanged. A refusal
// that is only a failed name lookup becomes a resolveError instead: it does not
// match ErrSSRFBlocked, and its cause stays retryable by the retry layer.
func terminalIfBlocked(err error) error {
	if err == nil || !isSSRFBlocked(err) {
		return err
	}
	if resolveCause(err) != nil {
		return asResolveError(err, "")
	}
	var already *blockedError
	if errors.As(err, &already) {
		return err
	}
	return &blockedError{cause: err}
}

// isSSRFBlocked reports whether err is a destination-guard refusal from either
// the go-kit policy or go-stealth's own guard sentinel.
func isSSRFBlocked(err error) bool {
	return errors.Is(err, kithttputil.ErrSSRFBlocked) || errors.Is(err, stealth.ErrSSRFBlocked)
}

// isRefusal reports whether err ends the fetch of this URL for every tier and
// fallback: a blocked destination or a destination that did not resolve.
func isRefusal(err error) bool {
	return isSSRFBlocked(err) || errors.Is(err, ErrResolveFailed)
}

// deadProxyAddr is the dial target of [emptyPoolProxy]: "this network", port 0.
const deadProxyAddr = "0.0.0.0:0"

// emptyPoolProxy is what [guardedPool] hands go-stealth in place of "", and the
// proxy the proxy client starts on. It is a safety net, not a route: [Fetcher]
// goes straight to the guarded direct client when the pool reports it is empty,
// and only a race (the pool running dry between that check and go-stealth's own
// Next) lands a request on this proxy.
//
// What happens to a request that does: go-kit's connect-time check refuses
// 0.0.0.0 on go-stealth versions that guard the proxy dial. Newer go-stealth
// allowlists a configured proxy endpoint, so the connect to port 0 fails with
// "connection refused" instead, and go-stealth retries it, which is slow; either
// way no connection to the destination is made. The dial error names
// [deadProxyAddr], which is how [isDeadProxyDial] attributes the failure to this
// call.
//
// Why a dead proxy rather than an unsupported one: tls-client's SetProxy rolls a
// failed switch back to the previous proxy, and with none set that is a bare
// direct dialer without the connect-time hook. Starting from a hooked dead proxy
// means no failure path, an empty pool or a malformed proxy URL alike, ends in an
// unguarded direct dial.
const emptyPoolProxy = "http://" + deadProxyAddr

// poolDryError marks a request whose dial went to [emptyPoolProxy]. It has no
// Unwrap, so the retry layer does not retry it (retrying would pay go-stealth's
// backoff against a dead proxy for nothing).
type poolDryError struct{ cause error }

func (e *poolDryError) Error() string { return "fetch: proxy pool had no proxy: " + e.cause.Error() }

// isDeadProxyDial reports whether err is a dial to the dead proxy, i.e. THIS
// request was pointed at [emptyPoolProxy]. A healthy proxied request that fails
// for its own reason (a 404, a 502 from the proxy, a timeout) never carries it.
func isDeadProxyDial(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Addr != nil && op.Addr.String() == deadProxyAddr
}

// guardedPool wraps the operator's proxy pool so it never returns "". go-stealth
// treats "" as "no proxy" and then dials the destination directly through a
// dialer that carries no connect-time hook, so a DNS rebind would go through.
// Instead an empty answer becomes emptyPoolProxy, the request fails, and the
// Fetcher retries it on a guarded direct client (see fetchViaProxy).
type guardedPool struct {
	proxypool.ProxyPool
}

func newGuardedPool(p proxypool.ProxyPool) *guardedPool { return &guardedPool{ProxyPool: p} }

// Next returns the inner pool's proxy, or emptyPoolProxy if it has none.
func (p *guardedPool) Next() string {
	if s := p.ProxyPool.Next(); s != "" {
		return s
	}
	return emptyPoolProxy
}

// empty reports whether the pool has no proxies at all.
func (p *guardedPool) empty() bool { return p != nil && p.Len() == 0 }
