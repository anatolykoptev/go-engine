package search

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// TestDualBrowser_TypedNilFallback_DoesNotPanic reproduces the 2026-05-16 prod panic:
// a typed-nil *HTTPDoer assigned to BrowserDoer interface is NOT == nil in Go,
// so the old `if fallback == nil` guard passed, wrapping it in dualBrowser,
// and calling d.fallback.Do() then panicked (nil pointer dereference).
func TestDualBrowser_TypedNilFallback_DoesNotPanic(t *testing.T) {
	primary := &stubDoer{status: 402, body: "Payment Required"}

	// Construct a typed-nil — this is the Go pitfall that caused the prod panic.
	var typedNil *HTTPDoer // typed nil: (*HTTPDoer)(nil)

	// Must NOT panic. newDualBrowser must detect the typed nil and return primary.
	d := newDualBrowser(primary, typedNil)

	// Calling Do must not panic either (d must be primary, not a dualBrowser with nil fallback).
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("TestDualBrowser_TypedNilFallback_DoesNotPanic panicked: %v", r)
		}
	}()
	_, _, _, _ = d.Do("GET", "http://x", nil, nil)
}

// TestIsNilInterface_TypedNilPointer exercises the isNilInterface helper across
// all cases that mattered for the prod panic fix.
func TestIsNilInterface_TypedNilPointer(t *testing.T) {
	var typedNilHTTPDoer *HTTPDoer     // typed nil pointer
	var typedNilStubDoer *stubDoer     // another typed nil
	var realValue BrowserDoer = &stubDoer{status: 200} // non-nil

	cases := []struct {
		name string
		v    any
		want bool
	}{
		{"untyped nil", nil, true},
		{"typed-nil *HTTPDoer", typedNilHTTPDoer, true},
		{"typed-nil *stubDoer", typedNilStubDoer, true},
		{"real value", realValue, false},
		{"int zero", 0, false},
		{"non-nil string", "hello", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isNilInterface(tc.v)
			if got != tc.want {
				t.Errorf("isNilInterface(%v) = %v, want %v", tc.v, got, tc.want)
			}
		})
	}
}

// TestDualBrowser_Fallback_LogsReason asserts that when the primary returns 402
// the slog output contains a reason attribute (e.g. "402").
func TestDualBrowser_Fallback_LogsReason(t *testing.T) {
	primary := &stubDoer{status: 402, body: "Payment Required"}
	fallback := &stubDoer{status: 200, body: "ok"}

	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	logger := slog.New(handler)

	d := newDualBrowserWithLogger(primary, fallback, logger)
	_, _, _, err := d.Do("GET", "http://x", nil, nil)
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}

	// Verify slog output contains a reason attribute.
	logOutput := buf.String()
	if logOutput == "" {
		t.Fatal("no log output emitted on fallback — expected slog.Warn with reason attr")
	}

	// Parse the JSON log line and check for "reason" field.
	var logEntry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(logOutput)), &logEntry); err != nil {
		t.Fatalf("log output is not valid JSON: %v\noutput: %s", err, logOutput)
	}

	reason, ok := logEntry["reason"]
	if !ok {
		t.Fatalf("log entry missing 'reason' field; got: %v", logEntry)
	}
	if reason != "402" {
		t.Errorf("reason = %q, want %q", reason, "402")
	}
}
