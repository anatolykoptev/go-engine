package sources_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anatolykoptev/go-engine/sources"
	"golang.org/x/time/rate"
)

// echoServer returns a test server that echoes request details as JSON.
type echoResponse struct {
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Query       map[string]string `json:"query"`
	Body        json.RawMessage   `json:"body,omitempty"`
	ContentType string            `json:"content_type"`
	Auth        string            `json:"auth"`
}

func newEchoServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := echoResponse{
			Method:      r.Method,
			Path:        r.URL.Path,
			ContentType: r.Header.Get("Content-Type"),
			Auth:        r.Header.Get("Authorization"),
			Query:       make(map[string]string),
		}
		for k, vs := range r.URL.Query() {
			resp.Query[k] = vs[0]
		}
		if r.ContentLength > 0 {
			var body json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				resp.Body = body
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func newErrorServer(t *testing.T, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal server error", statusCode)
	}))
}

// TestAPIClient_Get verifies that Get sends a GET with query params.
func TestAPIClient_Get(t *testing.T) {
	srv := newEchoServer(t)
	defer srv.Close()

	c := sources.NewAPIClient(srv.URL)

	params := url.Values{"q": {"golang"}, "limit": {"10"}}
	var got echoResponse
	if err := c.Get(context.Background(), "/search", params, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", got.Method)
	}
	if got.Path != "/search" {
		t.Errorf("path = %q, want /search", got.Path)
	}
	if got.Query["q"] != "golang" {
		t.Errorf("q = %q, want golang", got.Query["q"])
	}
	if got.Query["limit"] != "10" {
		t.Errorf("limit = %q, want 10", got.Query["limit"])
	}
}

// TestAPIClient_Post verifies that Post sends a POST with JSON body.
func TestAPIClient_Post(t *testing.T) {
	srv := newEchoServer(t)
	defer srv.Close()

	c := sources.NewAPIClient(srv.URL)

	reqBody := map[string]string{"query": "test", "lang": "en"}
	var got echoResponse
	if err := c.Post(context.Background(), "/query", reqBody, &got); err != nil {
		t.Fatalf("Post: %v", err)
	}

	if got.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.Method)
	}
	if !strings.Contains(got.ContentType, "application/json") {
		t.Errorf("content-type = %q, want application/json", got.ContentType)
	}

	var body map[string]string
	if err := json.Unmarshal(got.Body, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body["query"] != "test" {
		t.Errorf("body.query = %q, want test", body["query"])
	}
}

// TestAPIClient_BearerAuth verifies that WithAuth sets the Authorization header.
func TestAPIClient_BearerAuth(t *testing.T) {
	srv := newEchoServer(t)
	defer srv.Close()

	c := sources.NewAPIClient(srv.URL, sources.WithAuth(sources.BearerAuth("secret-token")))

	var got echoResponse
	if err := c.Get(context.Background(), "/protected", nil, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Auth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", got.Auth, "Bearer secret-token")
	}
}

// TestAPIClient_RateLimit verifies that WithRateLimit throttles requests.
func TestAPIClient_RateLimit(t *testing.T) {
	srv := newEchoServer(t)
	defer srv.Close()

	// 2 rps → each request after the first takes ~500ms; 3 requests ≥ ~500ms total.
	const rps = 2
	c := sources.NewAPIClient(srv.URL, sources.WithRateLimit(rate.NewLimiter(rps, 1)))

	start := time.Now()
	for range 3 {
		var got echoResponse
		if err := c.Get(context.Background(), "/", nil, &got); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	elapsed := time.Since(start)

	// At 2 rps with burst=1: request 1 is free, requests 2 and 3 each wait ~500ms.
	// So total ≥ ~1000ms. Give generous leeway: require ≥ 400ms.
	const minElapsed = 400 * time.Millisecond
	if elapsed < minElapsed {
		t.Errorf("elapsed = %v, want >= %v (rate limiting not working)", elapsed, minElapsed)
	}
}

// TestAPIClient_HTTPError verifies that non-2xx responses return an error.
func TestAPIClient_HTTPError(t *testing.T) {
	srv := newErrorServer(t, http.StatusInternalServerError)
	defer srv.Close()

	c := sources.NewAPIClient(srv.URL)

	var dest any
	err := c.Get(context.Background(), "/", nil, &dest)
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, want mention of 500", err.Error())
	}
}

// TestAPIClient_ContextCancellation verifies that a cancelled context aborts the request.
func TestAPIClient_ContextCancellation(t *testing.T) {
	// Use a server that hangs to ensure cancellation works.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until client disconnects.
		<-r.Context().Done()
		http.Error(w, "cancelled", http.StatusGatewayTimeout)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := sources.NewAPIClient(srv.URL)

	done := make(chan error, 1)
	go func() {
		var dest any
		done <- c.Get(ctx, "/slow", nil, &dest)
	}()

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error after context cancellation, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Error("Get did not return after context cancellation")
	}
}

// TestAPIClient_NilParams verifies Get works without query params.
func TestAPIClient_NilParams(t *testing.T) {
	srv := newEchoServer(t)
	defer srv.Close()

	c := sources.NewAPIClient(srv.URL)
	var got echoResponse
	if err := c.Get(context.Background(), "/no-params", nil, &got); err != nil {
		t.Fatalf("Get with nil params: %v", err)
	}
	if got.Path != "/no-params" {
		t.Errorf("path = %q, want /no-params", got.Path)
	}
}
