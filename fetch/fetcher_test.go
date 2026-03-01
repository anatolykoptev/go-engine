package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetcher_FetchBody_PlainHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>hello</body></html>"))
	}))
	defer srv.Close()

	f := New(WithTimeout(5 * time.Second))
	body, err := f.FetchBody(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	if string(body) != "<html><body>hello</body></html>" {
		t.Errorf("body = %q, want HTML content", body)
	}
}

func TestFetcher_FetchBody_Gzip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// httptest.Server auto-decompresses if Accept-Encoding is set,
		// so we test ReadResponseBody separately.
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("uncompressed content"))
	}))
	defer srv.Close()

	f := New(WithTimeout(5 * time.Second))
	body, err := f.FetchBody(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	if len(body) == 0 {
		t.Error("empty body")
	}
}

func TestFetcher_FetchBody_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	f := New(
		WithTimeout(5*time.Second),
		WithRetryConfig(RetryConfig{
			MaxRetries:  1,
			InitialWait: 10 * time.Millisecond,
			MaxWait:     50 * time.Millisecond,
			Multiplier:  1.5,
		}),
	)
	_, err := f.FetchBody(context.Background(), srv.URL)
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestFetcher_FetchBody_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Second) // hang forever
		_, _ = w.Write([]byte("late"))
	}))
	defer srv.Close()

	f := New(WithTimeout(10 * time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := f.FetchBody(ctx, srv.URL)
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestFetcher_HasProxy(t *testing.T) {
	f := New()
	if f.HasProxy() {
		t.Error("new fetcher without proxy should not have proxy")
	}

	// WithProxyPool(nil) should not set browser client.
	f2 := New(WithProxyPool(nil))
	if f2.HasProxy() {
		t.Error("fetcher with nil pool should not have proxy")
	}
}

func TestFetcher_ChromeHeaders(t *testing.T) {
	h := ChromeHeaders()
	if h == nil {
		t.Fatal("ChromeHeaders returned nil")
	}
	// Should have at least a few entries.
	if len(h) < 2 {
		t.Errorf("ChromeHeaders has only %d entries", len(h))
	}
}

func TestFetcher_RandomUserAgent(t *testing.T) {
	ua := RandomUserAgent()
	if ua == "" {
		t.Error("RandomUserAgent returned empty string")
	}
}

func TestFetcher_UserAgentHeader(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	f := New(WithTimeout(5 * time.Second))
	_, err := f.FetchBody(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	if gotUA == "" {
		t.Error("User-Agent header not set")
	}
}
