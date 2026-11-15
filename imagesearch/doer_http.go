package imagesearch

import (
	"io"
	"net/http"
	"time"

	kithttputil "github.com/anatolykoptev/go-kit/httputil"
)

const defaultHTTPTimeout = 15 * time.Second

// HTTPDoer wraps a standard *http.Client as a BrowserDoer.
// Does not use proxy or TLS fingerprinting — suitable as fallback
// for search engine AJAX endpoints that don't require stealth.
type HTTPDoer struct {
	Client *http.Client
}

// NewHTTPDoer creates a simple HTTP doer without proxy. The client carries the
// go-kit destination guard (connect-time check, every redirect hop, pre-request
// URL check), so a URL that targets an internal address is refused.
func NewHTTPDoer() *HTTPDoer {
	return &HTTPDoer{Client: kithttputil.NewSSRFGuardedClient(&http.Client{Timeout: defaultHTTPTimeout})}
}

func (d *HTTPDoer) Do(method, url string, headers map[string]string, body io.Reader) ([]byte, map[string]string, int, error) {
	req, err := http.NewRequest(method, url, body) //nolint:noctx // BrowserDoer interface carries no ctx (shared contract with websearch)
	if err != nil {
		return nil, nil, 0, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := d.Client.Do(req) //nolint:gosec // URL is caller-supplied by design
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, 0, err
	}
	rh := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		rh[k] = resp.Header.Get(k)
	}
	return data, rh, resp.StatusCode, nil
}
