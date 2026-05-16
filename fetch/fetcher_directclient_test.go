package fetch

import (
	"testing"
	"time"
)

// TestDirectClient_NilWithoutWithDirectFirst verifies that DirectClient() returns nil
// when WithDirectFirst(true) was not set (no direct tier configured).
func TestDirectClient_NilWithoutWithDirectFirst(t *testing.T) {
	f := New(WithTimeout(1 * time.Second))
	if got := f.DirectClient(); got != nil {
		t.Fatalf("DirectClient() = %v, want nil (WithDirectFirst not set)", got)
	}
}

// TestDirectClient_NonNilWithWithDirectFirst verifies that DirectClient() returns a
// non-nil *stealth.BrowserClient when WithDirectFirst(true) is set.
// This is the getter that go-search needs to wire direct-first correctly.
func TestDirectClient_NonNilWithWithDirectFirst(t *testing.T) {
	f := New(
		WithDirectFirst(true),
		WithTimeout(1*time.Second),
	)
	if got := f.DirectClient(); got == nil {
		t.Fatal("DirectClient() = nil, want *stealth.BrowserClient when WithDirectFirst(true)")
	}
}
