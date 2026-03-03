package search

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestErrRateLimited_Error(t *testing.T) {
	tests := []struct {
		name       string
		engine     string
		retryAfter time.Duration
		want       string
	}{
		{
			name:       "no retry after",
			engine:     "ddg",
			retryAfter: 0,
			want:       "rate limited by ddg",
		},
		{
			name:       "with retry after",
			engine:     "ddg",
			retryAfter: 30 * time.Second,
			want:       "rate limited by ddg (retry after 30s)",
		},
		{
			name:       "other engine no retry",
			engine:     "startpage",
			retryAfter: 0,
			want:       "rate limited by startpage",
		},
		{
			name:       "other engine with retry",
			engine:     "startpage",
			retryAfter: 2 * time.Minute,
			want:       "rate limited by startpage (retry after 2m0s)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &ErrRateLimited{Engine: tt.engine, RetryAfter: tt.retryAfter}
			got := err.Error()
			if got != tt.want {
				t.Errorf("ErrRateLimited.Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrRateLimited_ErrorsAs(t *testing.T) {
	original := &ErrRateLimited{Engine: "ddg", RetryAfter: 10 * time.Second}
	wrapped := fmt.Errorf("search failed: %w", original)

	var target *ErrRateLimited
	if !errors.As(wrapped, &target) {
		t.Fatal("errors.As should unwrap ErrRateLimited")
	}
	if target.Engine != "ddg" {
		t.Errorf("Engine = %q, want ddg", target.Engine)
	}
	if target.RetryAfter != 10*time.Second {
		t.Errorf("RetryAfter = %v, want 10s", target.RetryAfter)
	}
}

func TestIsDDGRateLimited(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want bool
	}{
		{
			name: "captcha form with action d.js and hidden input",
			body: []byte(`<html><body><form action="/d.js"><input type="hidden" name="v"><input type="submit"></form></body></html>`),
			want: true,
		},
		{
			name: "please try again text",
			body: []byte(`<html><body><p>Please try again later.</p></body></html>`),
			want: true,
		},
		{
			name: "not a robot text",
			body: []byte(`<html><body><h1>Are you not a robot?</h1></body></html>`),
			want: true,
		},
		{
			name: "unusual traffic text",
			body: []byte(`<html><body><p>We detected unusual traffic from your network.</p></body></html>`),
			want: true,
		},
		{
			name: "blocked text",
			body: []byte(`<html><body><p>Your IP has been blocked.</p></body></html>`),
			want: true,
		},
		{
			name: "normal results html",
			body: []byte(`<html><body><div class="result"><a class="result__a" href="https://example.com">Example</a><span class="result__snippet">A snippet.</span></div></body></html>`),
			want: false,
		},
		{
			name: "empty body",
			body: []byte{},
			want: false,
		},
		{
			name: "action d.js without hidden input",
			body: []byte(`<form action="/d.js"><input type="text"></form>`),
			want: false,
		},
		{
			name: "uppercase PLEASE TRY AGAIN",
			body: []byte(`<html><body>PLEASE TRY AGAIN</body></html>`),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDDGRateLimited(tt.body)
			if got != tt.want {
				t.Errorf("isDDGRateLimited() = %v, want %v (body: %s)", got, tt.want, tt.body)
			}
		})
	}
}

func TestIsStartpageRateLimited(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want bool
	}{
		{
			name: "rate limited in body",
			body: []byte(`<html><body><p>You have been rate limited.</p></body></html>`),
			want: true,
		},
		{
			name: "too many requests in body",
			body: []byte(`<html><body><h1>Too many requests</h1></body></html>`),
			want: true,
		},
		{
			name: "g-recaptcha in body",
			body: []byte(`<html><body><div class="g-recaptcha" data-sitekey="abc"></div></body></html>`),
			want: true,
		},
		{
			name: "captcha in body",
			body: []byte(`<html><body><form id="captcha-form"><input type="text"></form></body></html>`),
			want: true,
		},
		{
			name: "normal startpage results html",
			body: []byte(`<html><body><div class="w-gl__result"><a class="w-gl__result-title" href="https://example.com">Example</a><p class="w-gl__description">A snippet.</p></div></body></html>`),
			want: false,
		},
		{
			name: "empty body",
			body: []byte{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isStartpageRateLimited(tt.body)
			if got != tt.want {
				t.Errorf("isStartpageRateLimited() = %v, want %v (body: %s)", got, tt.want, tt.body)
			}
		})
	}
}
