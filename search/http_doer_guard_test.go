package search

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	kithttputil "github.com/anatolykoptev/go-kit/httputil"
)

// NewHTTPDoer is exported and dials whatever URL Do is given, so its client
// carries the destination guard: a loopback target is refused and never reached.
// Mutation: search/dual_browser.go NewHTTPDoer, return the bare http.Client
// without kithttputil.NewSSRFGuardedClient.
func TestNewHTTPDoer_RefusesLoopbackTarget(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	_, _, _, err := NewHTTPDoer().Do(http.MethodGet, srv.URL, nil, nil)
	if !errors.Is(err, kithttputil.ErrSSRFBlocked) {
		t.Fatalf("Do(loopback) error = %v, want errors.Is go-kit ErrSSRFBlocked", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("loopback server saw %d request(s), want 0", got)
	}
}
