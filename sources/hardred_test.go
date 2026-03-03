package sources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// --- Hard Red: APIClient concurrent access ---

func TestHR_APIClient_ConcurrentGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL, WithRateLimit(rate.NewLimiter(1000, 100)))

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var dest map[string]string
			_ = client.Get(context.Background(), "/test", nil, &dest)
		}()
	}
	wg.Wait()
}

func TestHR_APIClient_ConcurrentPost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var dest map[string]string
			_ = client.Post(context.Background(), "/test", map[string]string{"a": "b"}, &dest)
		}()
	}
	wg.Wait()
}

// --- Hard Red: cancelled context ---

func TestHR_APIClient_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	var dest map[string]string
	err := client.Get(ctx, "/slow", nil, &dest)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestHR_APIClient_RateLimitCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	// Very slow rate limiter: 1 token per second, burst 1.
	client := NewAPIClient(srv.URL, WithRateLimit(rate.NewLimiter(1, 1)))

	// Drain the token.
	var dest map[string]string
	_ = client.Get(context.Background(), "/drain", nil, &dest)

	// Next call with tight deadline should fail on rate limiter wait.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	err := client.Get(ctx, "/blocked", nil, &dest)
	if err == nil {
		t.Fatal("expected rate limit timeout error")
	}
}

// --- Hard Red: malformed responses ---

func TestHR_APIClient_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{not valid json`))
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL)
	var dest map[string]string
	err := client.Get(context.Background(), "/bad", nil, &dest)
	if err == nil {
		t.Fatal("expected decode error on malformed JSON")
	}
}

func TestHR_APIClient_EmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		// No body.
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL)
	var dest map[string]string
	err := client.Get(context.Background(), "/empty", nil, &dest)
	if err == nil {
		t.Fatal("expected error on empty body")
	}
}

func TestHR_APIClient_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL)
	var dest map[string]string
	err := client.Get(context.Background(), "/fail", nil, &dest)
	if err == nil {
		t.Fatal("expected error on HTTP 500")
	}
}

// --- Hard Red: Post with unmarshalable body ---

func TestHR_APIClient_PostUnmarshalableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL)
	var dest map[string]string
	// Channels can't be marshaled to JSON.
	err := client.Post(context.Background(), "/test", make(chan int), &dest)
	if err == nil {
		t.Fatal("expected marshal error")
	}
}

// --- Hard Red: auth methods applied correctly ---

func TestHR_BearerAuth_ConcurrentApply(t *testing.T) {
	auth := BearerAuth("secret-token")
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
			auth.Apply(req)
			got := req.Header.Get("Authorization")
			if got != "Bearer secret-token" {
				t.Errorf("Authorization = %q", got)
			}
		}()
	}
	wg.Wait()
}

// --- Hard Red: Query with special chars in params ---

func TestHR_APIClient_SpecialCharsInParams(t *testing.T) {
	var receivedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL)
	params := url.Values{
		"q":    {"hello world & more"},
		"lang": {"ru"},
	}
	var dest map[string]string
	err := client.Get(context.Background(), "/search", params, &dest)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if receivedQuery == "" {
		t.Fatal("query params not received")
	}
}
