package search

import (
	"context"
	"io"
	"net/http"
	"testing"
)

type sentJSONRequest struct {
	url     string
	headers map[string]string
}

func capturingJSON(sent *[]sentJSONRequest) *mockBrowser {
	return &mockBrowser{fn: func(_, u string, h map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		cp := make(map[string]string, len(h))
		for k, v := range h {
			cp[k] = v
		}
		*sent = append(*sent, sentJSONRequest{url: u, headers: cp})
		return []byte("{}"), nil, http.StatusOK, nil
	}}
}

// assertPageFetch pins the page-issued fetch() shape of a JSON API call to the
// page's own origin: cors, empty destination, same-origin, the page as
// Referer, no Origin, none of the document-only headers.
func assertPageFetch(t *testing.T, name string, r sentJSONRequest, wantReferer string) {
	t.Helper()
	h := r.headers
	if h["sec-fetch-mode"] != "cors" || h["sec-fetch-dest"] != "empty" || h["sec-fetch-site"] != "same-origin" {
		t.Errorf("%s: mode=%q dest=%q site=%q, want cors/empty/same-origin", name, h["sec-fetch-mode"], h["sec-fetch-dest"], h["sec-fetch-site"])
	}
	for _, k := range []string{"sec-fetch-user", "upgrade-insecure-requests", "origin"} {
		if v, ok := h[k]; ok {
			t.Errorf("%s: unexpected header %s=%q on a same-origin JSON fetch", name, k, v)
		}
	}
	if h["accept"] != "application/json" {
		t.Errorf("%s: accept=%q", name, h["accept"])
	}
	if h["referer"] != wantReferer {
		t.Errorf("%s: referer=%q, want %q", name, h["referer"], wantReferer)
	}
}

// TestJSONEngines_SendPageFetchHeaders pins the request shape of the Wikipedia
// and Marginalia JSON calls.
//
// Falsification: in search/wikipedia.go or search/marginalia.go replace
// websearch.ChromeFetchHeadersFor(bc, ...) with websearch.ChromeHeadersFor(bc)
// and the matching subtest goes RED (sec-fetch-mode "navigate", dest
// "document", plus upgrade-insecure-requests and sec-fetch-user on a JSON call).
func TestJSONEngines_SendPageFetchHeaders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("wikipedia", func(t *testing.T) {
		t.Parallel()
		var sent []sentJSONRequest
		_, _ = SearchWikipediaDirect(ctx, capturingJSON(&sent), "q", "de", nil)
		if len(sent) == 0 {
			t.Fatal("no request sent")
		}
		assertPageFetch(t, "wikipedia", sent[0], "https://de.wikipedia.org/")
	})

	t.Run("marginalia", func(t *testing.T) {
		t.Parallel()
		var sent []sentJSONRequest
		_, _ = searchMarginaliaDirect(ctx, capturingJSON(&sent), "q", "", nil)
		if len(sent) == 0 {
			t.Fatal("no request sent")
		}
		assertPageFetch(t, "marginalia", sent[0], "https://api.marginalia.nu/")
	})
}
