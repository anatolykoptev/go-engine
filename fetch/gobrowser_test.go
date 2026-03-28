package fetch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchViaGoBrowser_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/render" {
			t.Errorf("expected /render, got %s", r.URL.Path)
		}

		var req goBrowserRenderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.URL != "https://example.com" {
			t.Errorf("request URL = %q, want https://example.com", req.URL)
		}
		if req.TimeoutSec != 60 {
			t.Errorf("timeout_secs = %d, want 60", req.TimeoutSec)
		}

		resp := goBrowserRenderResponse{
			URL:   "https://example.com",
			HTML:  "<html><body>rendered</body></html>",
			Title: "Example",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	f := New(WithGoBrowserFallback(srv.URL))
	body, err := f.fetchViaGoBrowser(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("fetchViaGoBrowser: %v", err)
	}
	if string(body) != "<html><body>rendered</body></html>" {
		t.Errorf("body = %q, want rendered HTML", body)
	}
}

func TestFetchViaGoBrowser_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := goBrowserRenderResponse{
			Error: "timeout waiting for page load",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	f := New(WithGoBrowserFallback(srv.URL))
	_, err := f.fetchViaGoBrowser(context.Background(), "https://example.com")
	if err == nil {
		t.Fatal("expected error for error response")
	}
}

func TestFetchViaGoBrowser_EmptyHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := goBrowserRenderResponse{
			URL:   "https://example.com",
			HTML:  "",
			Title: "",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	f := New(WithGoBrowserFallback(srv.URL))
	_, err := f.fetchViaGoBrowser(context.Background(), "https://example.com")
	if err == nil {
		t.Fatal("expected error for empty HTML")
	}
}

func TestFetchViaGoBrowser_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	f := New(WithGoBrowserFallback(srv.URL))
	_, err := f.fetchViaGoBrowser(context.Background(), "https://example.com")
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestFetchBody_GoBrowserFallback(t *testing.T) {
	// Primary server always returns 500.
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	// go-browser mock returns rendered HTML.
	gbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := goBrowserRenderResponse{
			URL:   primary.URL,
			HTML:  "<html>fallback</html>",
			Title: "Fallback",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer gbSrv.Close()

	f := New(
		WithTimeout(2*time.Second),
		WithRetryConfig(RetryConfig{
			MaxRetries:  1,
			InitialWait: 10 * time.Millisecond,
			MaxWait:     50 * time.Millisecond,
			Multiplier:  1.5,
		}),
		WithGoBrowserFallback(gbSrv.URL),
	)

	body, err := f.FetchBody(context.Background(), primary.URL)
	if err != nil {
		t.Fatalf("FetchBody with go-browser fallback: %v", err)
	}
	if string(body) != "<html>fallback</html>" {
		t.Errorf("body = %q, want fallback HTML", body)
	}
}

func TestWithGoBrowserFallback_EmptyURL(t *testing.T) {
	f := New(WithGoBrowserFallback(""))
	if f.goBrowserURL != "" {
		t.Error("empty URL should not set goBrowserURL")
	}
}
