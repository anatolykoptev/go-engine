// Package fetch provides HTTP fetching with retry, proxy rotation,
// and TLS fingerprint impersonation via go-stealth.
//
// The primary entry point is [Fetcher], which routes requests through
// a residential proxy (BrowserClient) when available, falling back to
// a standard HTTP client.
package fetch
