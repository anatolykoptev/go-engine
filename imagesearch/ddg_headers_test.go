package imagesearch

import (
	"context"
	"io"
	"net/http"
	"testing"
)

type captureDoer struct {
	sent []map[string]string
	body []string
}

func (c *captureDoer) Do(_, _ string, h map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
	cp := make(map[string]string, len(h))
	for k, v := range h {
		cp[k] = v
	}
	c.sent = append(c.sent, cp)
	i := len(c.sent) - 1
	if i < len(c.body) {
		return []byte(c.body[i]), nil, http.StatusOK, nil
	}
	return []byte("{}"), nil, http.StatusOK, nil
}

// TestDdgImages_RequestShapePerStep pins the two DDG image requests: the
// homepage navigation that carries a Referer on the engine's own origin
// (same-origin, never none), then the page's i.js fetch() (cors, empty,
// same-origin, no document-only headers, no accept-encoding).
//
// Falsification, at the call sites in imagesearch/ddg.go: swap
// searchHeadersForSite(doer, ...) for searchHeadersFor(doer) and the first
// assertion goes RED (sec-fetch-site none with a Referer); swap
// fetchHeadersFor(doer) for searchHeadersFor(doer) and the second goes RED
// (sec-fetch-mode navigate, dest document on the JSON call).
func TestDdgImages_RequestShapePerStep(t *testing.T) {
	t.Parallel()
	d := &captureDoer{body: []string{`<script>vqd="4-123456789"</script>`}}
	_, _ = (&DdgImages{}).Search(context.Background(), d, "q", 5)
	if len(d.sent) != 2 {
		t.Fatalf("sent %d requests, want 2", len(d.sent))
	}

	nav := d.sent[0]
	if nav["sec-fetch-mode"] != "navigate" || nav["sec-fetch-site"] != "same-origin" {
		t.Errorf("homepage: mode=%q site=%q, want navigate/same-origin", nav["sec-fetch-mode"], nav["sec-fetch-site"])
	}
	if nav["referer"] != "" && nav["sec-fetch-site"] == "none" {
		t.Errorf("homepage: sec-fetch-site none with Referer %q", nav["referer"])
	}

	api := d.sent[1]
	if api["sec-fetch-mode"] != "cors" || api["sec-fetch-dest"] != "empty" || api["sec-fetch-site"] != "same-origin" {
		t.Errorf("i.js: mode=%q dest=%q site=%q, want cors/empty/same-origin", api["sec-fetch-mode"], api["sec-fetch-dest"], api["sec-fetch-site"])
	}
	for _, k := range []string{"sec-fetch-user", "upgrade-insecure-requests", "accept-encoding"} {
		if v, ok := api[k]; ok {
			t.Errorf("i.js: unexpected header %s=%q", k, v)
		}
	}
}
