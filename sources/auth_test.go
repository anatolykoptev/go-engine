package sources_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/anatolykoptev/go-engine/sources"
)

// TestAuthMethod_Interface verifies both auth types satisfy AuthMethod at compile time.
func TestAuthMethod_Interface(t *testing.T) {
	var _ = sources.BearerAuth("token")
	var _ = sources.NoAuthMethod()
}

// TestBearerAuth_Apply verifies that BearerAuth sets the Authorization header.
func TestBearerAuth_Apply(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{"simple token", "abc123", "Bearer abc123"},
		{"long token", "eyJhbGciOiJSUzI1NiJ9.payload.sig", "Bearer eyJhbGciOiJSUzI1NiJ9.payload.sig"},
		{"empty token", "", "Bearer "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com", nil)
			auth := sources.BearerAuth(tt.token)
			auth.Apply(req)
			got := req.Header.Get("Authorization")
			if got != tt.want {
				t.Errorf("Authorization = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBearerAuth_OverwritesExisting verifies that BearerAuth replaces any existing Authorization header.
func TestBearerAuth_OverwritesExisting(t *testing.T) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com", nil)
	req.Header.Set("Authorization", "Basic oldcred")

	sources.BearerAuth("newtoken").Apply(req)

	got := req.Header.Get("Authorization")
	want := "Bearer newtoken"
	if got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}

// TestNoAuth_Apply verifies that NoAuthMethod does not modify the request.
func TestNoAuth_Apply(t *testing.T) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com", nil)
	// Set a header to confirm it is not touched.
	req.Header.Set("Authorization", "Bearer existing")

	sources.NoAuthMethod().Apply(req)

	got := req.Header.Get("Authorization")
	want := "Bearer existing"
	if got != want {
		t.Errorf("Authorization = %q, want %q (NoAuth should not modify headers)", got, want)
	}
}

// TestNoAuth_EmptyRequest verifies NoAuthMethod works on a request with no headers.
func TestNoAuth_EmptyRequest(t *testing.T) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com", nil)
	sources.NoAuthMethod().Apply(req)

	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty", got)
	}
}
