package fetch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestFetchBody_OxBrowserSendsInternalSecret asserts the full consumer path
// (New + WithOxBrowser + FetchBody fallback) sends X-Internal-Secret from
// INTERNAL_SERVICE_SECRET to the ox-browser origin. Mutation probe: remove
// the svcauth.FromEnv wrap in fetchViaOxBrowser → stand-in sees no header →
// assertion fails (RED).
func TestFetchBody_OxBrowserSendsInternalSecret(t *testing.T) {
	t.Setenv("INTERNAL_SERVICE_SECRET", "s3cret")

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	oxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Internal-Secret"); got != "s3cret" {
			t.Errorf("X-Internal-Secret = %q, want %q", got, "s3cret")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(oxFetchResponse{
			Status: 200, Body: "<html>authed</html>",
		})
	}))
	defer oxSrv.Close()

	f := New(
		WithTimeout(2*time.Second),
		WithRetryConfig(RetryConfig{
			MaxRetries:  1,
			InitialWait: 10 * time.Millisecond,
			MaxWait:     50 * time.Millisecond,
			Multiplier:  1.5,
		}),
		WithOxBrowser(oxSrv.URL),
	)

	body, err := f.FetchBody(context.Background(), primary.URL)
	if err != nil {
		t.Fatalf("FetchBody with ox-browser fallback: %v", err)
	}
	if string(body) != "<html>authed</html>" {
		t.Errorf("body = %q, want fallback HTML", body)
	}
}

// TestFetchViaOxBrowser_RedirectDropsInternalSecret asserts the secret does not
// leak off-origin: when ox-browser answers 302 to a second origin, the
// redirected request must carry NO X-Internal-Secret. Mutation probe: remove
// the svcauth.FromEnv wrap → stdlib http.Client copies the header onto the
// cross-origin redirect → second origin sees it → assertion fails (RED).
func TestFetchViaOxBrowser_RedirectDropsInternalSecret(t *testing.T) {
	t.Setenv("INTERNAL_SERVICE_SECRET", "s3cret")

	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Internal-Secret"); got != "" {
			t.Errorf("redirected request carried X-Internal-Secret = %q, want empty", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(oxFetchResponse{
			Status: 200, Body: "<html>redirected</html>",
		})
	}))
	defer second.Close()

	oxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second.URL, http.StatusFound)
	}))
	defer oxSrv.Close()

	f := New(WithOxBrowser(oxSrv.URL))
	got, err := f.fetchViaOxBrowser(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("fetchViaOxBrowser: %v", err)
	}
	if string(got) != "<html>redirected</html>" {
		t.Errorf("body = %q, want redirected body", got)
	}
}

// TestFetchViaOxBrowser_NoInternalSecretWhenEnvUnset asserts the env-unset
// case stays byte-identical to today: no header is sent and the request still
// succeeds (svcauth skips routes with an empty secret).
func TestFetchViaOxBrowser_NoInternalSecretWhenEnvUnset(t *testing.T) {
	t.Setenv("INTERNAL_SERVICE_SECRET", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Internal-Secret"); got != "" {
			t.Errorf("X-Internal-Secret = %q, want empty when env unset", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(oxFetchResponse{
			Status: 200, Body: "<html>ok</html>",
		})
	}))
	defer srv.Close()

	f := New(WithOxBrowser(srv.URL))
	got, err := f.fetchViaOxBrowser(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("fetchViaOxBrowser: %v", err)
	}
	if string(got) != "<html>ok</html>" {
		t.Errorf("body = %q, want fallback HTML", got)
	}
}
