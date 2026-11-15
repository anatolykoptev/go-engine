package websearch

import (
	"context"
	"io"
	"net/http"
	"testing"
)

type sentRequest struct {
	method  string
	url     string
	headers map[string]string
}

// capturing returns a BrowserDoer that records every request and answers 200
// with an empty body; the engines under test fail to parse it, which these
// tests ignore: they read the request, not the result.
func capturing(sent *[]sentRequest) *mockBrowser {
	return &mockBrowser{fn: func(method, u string, h map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		cp := make(map[string]string, len(h))
		for k, v := range h {
			cp[k] = v
		}
		*sent = append(*sent, sentRequest{method: method, url: u, headers: cp})
		return []byte("{}"), nil, http.StatusOK, nil
	}}
}

// assertCoherent pins what real Chrome never contradicts: a request that
// carries a Referer is not a typed navigation (sec-fetch-site is not "none"),
// and a POST carries an Origin.
func assertCoherent(t *testing.T, name string, r sentRequest) {
	t.Helper()
	if r.headers["referer"] != "" && r.headers["sec-fetch-site"] == "none" {
		t.Errorf("%s: sec-fetch-site none with Referer %q", name, r.headers["referer"])
	}
	if r.method == http.MethodPost && r.headers["origin"] == "" {
		t.Errorf("%s: POST without Origin", name)
	}
}

// assertXHR pins the page-issued fetch() shape: cors mode, empty destination,
// none of the document-only headers, a JSON Accept.
func assertXHR(t *testing.T, name string, r sentRequest, wantSite string) {
	t.Helper()
	h := r.headers
	if h["sec-fetch-mode"] != "cors" || h["sec-fetch-dest"] != "empty" {
		t.Errorf("%s: sec-fetch-mode=%q dest=%q, want cors/empty", name, h["sec-fetch-mode"], h["sec-fetch-dest"])
	}
	if h["sec-fetch-site"] != wantSite {
		t.Errorf("%s: sec-fetch-site=%q, want %q", name, h["sec-fetch-site"], wantSite)
	}
	for _, k := range []string{"sec-fetch-user", "upgrade-insecure-requests"} {
		if v, ok := h[k]; ok {
			t.Errorf("%s: document-only header %s=%q on a JSON request", name, k, v)
		}
	}
	assertCoherent(t, name, r)
}

// assertForm pins the form-POST navigation shape: navigate mode, document
// destination, same-origin, the page as Referer and its Origin.
func assertForm(t *testing.T, name string, r sentRequest, wantOrigin string) {
	t.Helper()
	h := r.headers
	if r.method != http.MethodPost {
		t.Fatalf("%s: method %s, want POST", name, r.method)
	}
	if h["sec-fetch-mode"] != "navigate" || h["sec-fetch-dest"] != "document" || h["sec-fetch-site"] != "same-origin" {
		t.Errorf("%s: mode=%q dest=%q site=%q, want navigate/document/same-origin", name, h["sec-fetch-mode"], h["sec-fetch-dest"], h["sec-fetch-site"])
	}
	if h["origin"] != wantOrigin {
		t.Errorf("%s: origin=%q, want %q", name, h["origin"], wantOrigin)
	}
	assertCoherent(t, name, r)
}

// TestEngines_RequestShapePerCallSite pins, per engine call site, the
// Sec-Fetch relationship the request really has: JSON endpoints are page
// fetch() calls (cors, empty, same-origin or same-site, no document-only
// headers), form POSTs carry same-origin and Origin, and no request pairs a
// Referer with sec-fetch-site none.
//
// Falsification, each at its call site (RED, the message names the engine):
//   - reddit.go, yep.go, ddg.go searchDJS: replace ChromeFetchHeadersFor with
//     ChromeHeadersFor (the document-navigation set on a JSON call).
//   - ddg.go searchHTML and getVQD, startpage.go, brave.go: replace
//     ChromeHeadersForSite with ChromeHeadersFor (sec-fetch-site none with a
//     Referer).
func TestEngines_RequestShapePerCallSite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("reddit", func(t *testing.T) {
		t.Parallel()
		var sent []sentRequest
		_, _ = NewReddit(WithRedditBrowser(capturing(&sent))).Search(ctx, "q", SearchOpts{})
		if len(sent) == 0 {
			t.Fatal("no request sent")
		}
		assertXHR(t, "reddit", sent[0], "same-origin")
		if sent[0].headers["accept"] != acceptJSON || sent[0].headers["referer"] == "" {
			t.Errorf("reddit: accept=%q referer=%q", sent[0].headers["accept"], sent[0].headers["referer"])
		}
		if _, ok := sent[0].headers["origin"]; ok {
			t.Error("reddit: same-origin GET carries Origin")
		}
	})

	t.Run("yep", func(t *testing.T) {
		t.Parallel()
		var sent []sentRequest
		_, _ = NewYep(WithYepBrowser(capturing(&sent))).Search(ctx, "q", SearchOpts{})
		if len(sent) == 0 {
			t.Fatal("no request sent")
		}
		assertXHR(t, "yep", sent[0], "same-site")
		if sent[0].headers["origin"] != "https://yep.com" {
			t.Errorf("yep: origin=%q", sent[0].headers["origin"])
		}
	})

	t.Run("ddg", func(t *testing.T) {
		t.Parallel()
		var sent []sentRequest
		d, err := NewDDG(WithDDGBrowser(capturing(&sent)))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = d.searchHTML(ctx, "q", "wt-wt", "")
		_, _ = d.getVQD(ctx, "q")
		_, _ = d.searchDJS(ctx, "q", "vqd", "wt-wt", "")
		if len(sent) != 3 {
			t.Fatalf("sent %d requests, want 3", len(sent))
		}
		assertForm(t, "ddg html", sent[0], "https://html.duckduckgo.com")
		if sent[1].headers["sec-fetch-site"] != "same-origin" || sent[1].headers["sec-fetch-mode"] != "navigate" {
			t.Errorf("ddg vqd: sec-fetch-site=%q mode=%q, want same-origin/navigate", sent[1].headers["sec-fetch-site"], sent[1].headers["sec-fetch-mode"])
		}
		assertCoherent(t, "ddg vqd", sent[1])
		assertXHR(t, "ddg d.js", sent[2], "same-site")
		if sent[2].headers["origin"] != "https://duckduckgo.com" {
			t.Errorf("ddg d.js: origin=%q", sent[2].headers["origin"])
		}
	})

	t.Run("startpage", func(t *testing.T) {
		t.Parallel()
		var sent []sentRequest
		_, _ = NewStartpage(WithStartpageBrowser(capturing(&sent))).Search(ctx, "q", SearchOpts{})
		if len(sent) == 0 {
			t.Fatal("no request sent")
		}
		assertForm(t, "startpage", sent[0], "https://www.startpage.com")
	})

	t.Run("brave", func(t *testing.T) {
		t.Parallel()
		var sent []sentRequest
		_, _ = NewBrave(WithBraveBrowser(capturing(&sent))).Search(ctx, "q", SearchOpts{})
		if len(sent) == 0 {
			t.Fatal("no request sent")
		}
		h := sent[0].headers
		if h["sec-fetch-mode"] != "navigate" || h["sec-fetch-site"] != "same-origin" {
			t.Errorf("brave: mode=%q site=%q, want navigate/same-origin", h["sec-fetch-mode"], h["sec-fetch-site"])
		}
		assertCoherent(t, "brave", sent[0])
	})
}
