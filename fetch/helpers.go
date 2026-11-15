package fetch

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"

	stealth "github.com/anatolykoptev/go-stealth"

	"github.com/anatolykoptev/go-engine/internal/chromehdr"
)

// ReadResponseBody reads the response body, handling gzip decompression.
func ReadResponseBody(resp *http.Response) ([]byte, error) {
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		return io.ReadAll(gz)
	}
	return io.ReadAll(resp.Body)
}

// ChromeHeaders returns the Chrome navigation headers for go-stealth's default
// identity: its User-Agent, Client Hints and the full navigation set.
func ChromeHeaders() map[string]string {
	return chromehdr.For(nil)
}

// RandomUserAgent returns the User-Agent of a random desktop OS variant of the
// default Chrome profile, so it always matches the default TLS fingerprint.
func RandomUserAgent() string {
	return stealth.RandomChromeUserAgent()
}

// RetryDo retries fn up to MaxRetries times with exponential backoff.
func RetryDo[T any](ctx context.Context, rc RetryConfig, fn func() (T, error)) (T, error) {
	return stealth.RetryDo(ctx, rc, fn)
}

// RetryHTTP executes an HTTP request function with retry logic.
func RetryHTTP(ctx context.Context, rc RetryConfig, fn func() (*http.Response, error)) (*http.Response, error) {
	return stealth.RetryHTTP(ctx, rc, fn)
}
