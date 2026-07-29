package search

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anatolykoptev/go-engine/metrics"
)

func TestParseMarginaliaJSON_HappyPath(t *testing.T) {
	data := []byte(`{"results":[{"url":"https://example.com","title":"Test Page","description":"A test page","quality":3.5},{"url":"https://example2.com","title":"Another Page","description":"Another page","quality":2.1}]}`)
	results, err := ParseMarginaliaJSON(data)
	if err != nil {
		t.Fatalf("ParseMarginaliaJSON: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Title != "Test Page" {
		t.Errorf("results[0].Title = %q, want Test Page", results[0].Title)
	}
	if results[0].URL != "https://example.com" {
		t.Errorf("results[0].URL = %q, want https://example.com", results[0].URL)
	}
	if results[0].Content != "A test page" {
		t.Errorf("results[0].Content = %q, want A test page", results[0].Content)
	}
	if results[0].Metadata["engine"] != "marginalia" {
		t.Errorf("Metadata[engine] = %q, want marginalia", results[0].Metadata["engine"])
	}
	if results[1].Title != "Another Page" {
		t.Errorf("results[1].Title = %q, want Another Page", results[1].Title)
	}
}

func TestParseMarginaliaJSON_Empty(t *testing.T) {
	data := []byte(`{"results":[]}`)
	results, err := ParseMarginaliaJSON(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestSearchMarginaliaDirect_NonOK(t *testing.T) {
	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return nil, nil, http.StatusInternalServerError, nil
	}}
	_, err := searchMarginaliaDirect(context.Background(), bc, "golang", "", nil)
	if err == nil {
		t.Error("expected error on 500, got nil")
	}
}

func TestSearchMarginaliaDirect_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return []byte(`{"results":[]}`), nil, http.StatusOK, nil
	}}
	_, err := searchMarginaliaDirect(ctx, bc, "golang", "", nil)
	if err == nil {
		t.Error("expected error on cancelled context, got nil")
	}
}

func TestSearchMarginaliaDirect_RateLimit(t *testing.T) {
	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return nil, nil, http.StatusTooManyRequests, nil
	}}
	results, err := searchMarginaliaDirect(context.Background(), bc, "golang", "", nil)
	if err != nil {
		t.Errorf("expected nil error on 429, got: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results on 429")
	}
}

// captureURIBrowser is a mockBrowser that records the request URL and returns
// a minimal valid Marginalia response. Used to assert the key path segment.
func captureURIBrowser(captured *string) *mockBrowser {
	return &mockBrowser{fn: func(_, u string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		*captured = u
		return []byte(`{"results":[]}`), nil, http.StatusOK, nil
	}}
}

// TestSearchMarginaliaDirect_KeyInPath asserts a configured key appears as the
// path segment before /search/, and an unset (empty) key yields "public".
//
// RED-ON-REVERT: hardcode "public" in searchMarginaliaDirect (ignore the key
// arg) → the configured-key assertion fails (path contains /public/ not
// /personal-key/), while the unset-key assertion still passes.
func TestSearchMarginaliaDirect_KeyInPath(t *testing.T) {
	var got string
	bc := captureURIBrowser(&got)
	if _, err := searchMarginaliaDirect(context.Background(), bc, "golang", "personal-key", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "://api.marginalia.nu/personal-key/search/golang") {
		t.Errorf("configured key: URL %q does not contain /personal-key/search/", got)
	}

	got = ""
	bc = captureURIBrowser(&got)
	if _, err := searchMarginaliaDirect(context.Background(), bc, "golang", "", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "://api.marginalia.nu/public/search/golang") {
		t.Errorf("unset key: URL %q does not contain /public/search/", got)
	}
}

// TestSearchMarginaliaDirect_KeyEscaping asserts a key containing a path-unsafe
// character is url.PathEscape'd so the URL stays well-formed (the slash does
// not become an extra path segment that silently hits a different endpoint).
//
// RED-ON-REVERT: drop the url.PathEscape on the key (concatenate raw) → the
// slash survives into the URL → strings.Contains finds the raw "a/b" segment
// AND the URL parses with an extra segment → the well-formed assertion fails.
func TestSearchMarginaliaDirect_KeyEscaping(t *testing.T) {
	var got string
	bc := captureURIBrowser(&got)
	if _, err := searchMarginaliaDirect(context.Background(), bc, "golang", "a/b", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("escaped key produced unparseable URL %q: %v", got, err)
	}
	// PathEscape encodes "/" as "%2F"; the raw slash must NOT appear in the
	// key segment, otherwise it would split into an extra path component.
	wantSeg := "/a%2Fb/search/golang"
	if !strings.Contains(u.EscapedPath(), wantSeg) {
		t.Errorf("escaped key: path %q does not contain %q", u.EscapedPath(), wantSeg)
	}
}

// TestSearchMarginaliaDirect_NoKeyLeakInError asserts that when bc.Do returns
// a *url.Error (as Go's http.Client does, carrying the full URL — and thus the
// key — in its message), the error returned to the caller does NOT contain the
// key. This is the URL-leak finding: the prior `return nil, err` forwarded the
// raw *url.Error verbatim.
//
// RED-ON-REVERT: remove the errors.As(*url.Error) stripping branch and return
// the raw err → the error string contains the key → the assertion fails.
func TestSearchMarginaliaDirect_NoKeyLeakInError(t *testing.T) {
	const fakeKey = "leak-probe-key"
	ue := &url.Error{Op: "Get", URL: "https://api.marginalia.nu/" + fakeKey + "/search/golang", Err: errors.New("dial tcp: connection refused")}
	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return nil, nil, 0, ue
	}}
	_, err := searchMarginaliaDirect(context.Background(), bc, "golang", fakeKey, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), fakeKey) {
		t.Errorf("key leaked into error message: %q", err.Error())
	}
}

// TestRunMarginalia_BudgetSheds asserts that once the daily budget is
// exhausted, runMarginalia (a) returns ErrMarginaliaQuotaExhausted, (b) never
// issues the outbound HTTP request, and (c) returns promptly without blocking.
//
// RED-ON-REVERT: remove the budget.Acquire guard in runMarginalia → the
// browser IS called → bc.called > 0 → test fails; also the returned error is
// nil (200 path) not ErrMarginaliaQuotaExhausted → the errors.Is assertion
// fails.
func TestRunMarginalia_BudgetSheds(t *testing.T) {
	m := metrics.New()
	budget := NewMarginaliaBudget(1, m) // 1-query budget
	bc := &stubDoer{status: 200, body: `{"results":[]}`}

	// First call consumes the single allowed query.
	cfg := DirectConfig{Browser: bc, MarginaliaBudget: budget, Metrics: m}
	if _, err := runMarginalia(context.Background(), cfg, "golang"); err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	if bc.called != 1 {
		t.Fatalf("first call: expected 1 browser call, got %d", bc.called)
	}

	// Second call must shed without issuing a request.
	start := time.Now()
	res, err := runMarginalia(context.Background(), cfg, "golang")
	elapsed := time.Since(start)
	if !errors.Is(err, ErrMarginaliaQuotaExhausted) {
		t.Fatalf("second call: expected ErrMarginaliaQuotaExhausted, got %v", err)
	}
	if res != nil {
		t.Errorf("second call: expected nil results, got %v", res)
	}
	if bc.called != 1 {
		t.Errorf("second call: browser must NOT be called on shed, got called=%d", bc.called)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("shed must be non-blocking; took %v", elapsed)
	}
}

// TestRunMarginalia_BudgetMetric asserts the remaining-budget gauge reflects
// consumption, using an ANCHORED assertion on the exported gauge value (not a
// substring of a formatted snapshot).
//
// RED-ON-REVERT: remove the publishRemaining call in Acquire → the gauge is
// never set → Value() returns 0 → the == 2 assertion after one acquire fails.
func TestRunMarginalia_BudgetMetric(t *testing.T) {
	m := metrics.New()
	budget := NewMarginaliaBudget(3, m)
	bc := &stubDoer{status: 200, body: `{"results":[]}`}
	cfg := DirectConfig{Browser: bc, MarginaliaBudget: budget, Metrics: m}

	if got := m.Gauge(metricMarginaliaBudgetRemaining).Value(); got != 3 {
		t.Fatalf("initial remaining: got %v, want 3", got)
	}
	if _, err := runMarginalia(context.Background(), cfg, "golang"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if got := m.Gauge(metricMarginaliaBudgetRemaining).Value(); got != 2 {
		t.Errorf("after 1 consume: remaining got %v, want 2", got)
	}
	if _, err := runMarginalia(context.Background(), cfg, "golang"); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if _, err := runMarginalia(context.Background(), cfg, "golang"); err != nil {
		t.Fatalf("third call: %v", err)
	}
	if got := m.Gauge(metricMarginaliaBudgetRemaining).Value(); got != 0 {
		t.Errorf("after 3 consumes: remaining got %v, want 0", got)
	}
	// 4th call sheds; remaining stays 0.
	if _, err := runMarginalia(context.Background(), cfg, "golang"); !errors.Is(err, ErrMarginaliaQuotaExhausted) {
		t.Fatalf("fourth call: expected ErrMarginaliaQuotaExhausted, got %v", err)
	}
	if got := m.Gauge(metricMarginaliaBudgetRemaining).Value(); got != 0 {
		t.Errorf("after shed: remaining got %v, want 0", got)
	}
}

// TestMarginaliaBudget_CalendarReset asserts the counter resets at the UTC
// calendar-day boundary (not a rolling window). Uses an injectable clock.
//
// RED-ON-REVERT: remove the today != b.day reset branch in Acquire → the
// counter never resets → the post-rollover Acquire returns false (still
// spent=limit) → the assertion fails.
func TestMarginaliaBudget_CalendarReset(t *testing.T) {
	now := time.Date(2026, 7, 29, 23, 59, 0, 0, time.UTC)
	b := newMarginaliaBudget(2, nil, func() time.Time { return now })

	if !b.Acquire() {
		t.Fatal("acquire 1 within day should succeed")
	}
	if !b.Acquire() {
		t.Fatal("acquire 2 within day should succeed")
	}
	if b.Acquire() {
		t.Fatal("acquire 3 within same day should be shed")
	}

	// Cross the UTC midnight boundary: same instant, next calendar day.
	now = time.Date(2026, 7, 30, 0, 1, 0, 0, time.UTC)
	if !b.Acquire() {
		t.Fatal("acquire after UTC midnight should succeed (counter reset)")
	}
}

// TestParseMarginaliaJSON_LicenseAttribution asserts the license field is
// surfaced into result Metadata (Part C — the credit/link-back we promised).
//
// RED-ON-REVERT: drop the License field from marginaliaResp or the
// md["license"] assignment → Metadata["license"] == "" → assertion fails.
func TestParseMarginaliaJSON_LicenseAttribution(t *testing.T) {
	data := []byte(`{"license":"CC-BY-NC-SA 4.0","results":[{"url":"https://example.com","title":"T","description":"d","quality":1}]}`)
	results, err := ParseMarginaliaJSON(data)
	if err != nil {
		t.Fatalf("ParseMarginaliaJSON: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if got := results[0].Metadata["license"]; got != "CC-BY-NC-SA 4.0" {
		t.Errorf("Metadata[license] = %q, want CC-BY-NC-SA 4.0", got)
	}
	if got := results[0].Metadata["source_url"]; got != "https://search.marginalia.nu/" {
		t.Errorf("Metadata[source_url] = %q, want https://search.marginalia.nu/", got)
	}
	if got := results[0].Metadata["engine"]; got != "marginalia" {
		t.Errorf("Metadata[engine] = %q, want marginalia", got)
	}
}

// TestSearchMarginaliaDirect_WhitespaceKeyDefaultsToPublic asserts a
// whitespace-only key is trimmed before the empty check and falls back to
// "public" instead of escaping to "%20" and producing a 404.
//
// RED-ON-REVERT: remove the strings.TrimSpace before the empty check → " "
// survives → url.PathEscape(" ") = "%20" → the URL contains "%20" not
// "public" → the assertion fails.
func TestSearchMarginaliaDirect_WhitespaceKeyDefaultsToPublic(t *testing.T) {
	var got string
	bc := captureURIBrowser(&got)
	if _, err := searchMarginaliaDirect(context.Background(), bc, "golang", "   ", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "://api.marginalia.nu/public/search/golang") {
		t.Errorf("whitespace key: URL %q does not contain /public/search/ (key not trimmed)", got)
	}
	if strings.Contains(got, "%20") {
		t.Errorf("whitespace key: URL %q contains %%20 (key escaped instead of trimmed)", got)
	}
}

// TestSearchMarginaliaDirect_NoKeyLeakInPlainError asserts that when bc.Do
// returns a plain error (NOT a *url.Error) whose text embeds the request URL,
// the key is still redacted from the returned error string. The prior code
// only stripped *url.Error; a custom BrowserDoer returning errors.New(url)
// would leak the key through handleSourceError logging.
//
// RED-ON-REVERT: revert to the errors.As(*url.Error)-only branch → the plain
// error passes through verbatim → the key is in err.Error() → the assertion
// fails.
func TestSearchMarginaliaDirect_NoKeyLeakInPlainError(t *testing.T) {
	const fakeKey = "plain-leak-probe-key"
	// A plain error (not *url.Error) whose message contains the full URL —
	// simulates a custom BrowserDoer that embeds the request URL in its error.
	plainErr := errors.New(`Get "https://api.marginalia.nu/` + fakeKey + `/search/golang": connection refused`)
	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return nil, nil, 0, plainErr
	}}
	_, err := searchMarginaliaDirect(context.Background(), bc, "golang", fakeKey, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), fakeKey) {
		t.Errorf("key leaked into plain-error message: %q", err.Error())
	}
	// The underlying error chain must be preserved so handleSourceError can
	// still classify context errors via errors.Is.
	if !errors.Is(err, plainErr) {
		t.Errorf("error chain broken: errors.Is(err, plainErr) = false")
	}
}

// TestMarginalia_HTTPCallerNotExported is a structural test asserting the
// function that issues the Marginalia HTTP call (contains the api.marginalia.nu
// URL literal) is unexported. This is the F2 defense-in-depth invariant: the
// HTTP caller is package-private so runMarginalia (with its budget guard) is
// the sole entry point — no external code can bypass the quota check.
//
// RED-ON-REVERT: re-export the function (rename searchMarginaliaDirect →
// SearchMarginaliaDirect) → fn.Name.IsExported() returns true → the assertion
// fails.
func TestMarginalia_HTTPCallerNotExported(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "marginalia.go", nil, 0)
	if err != nil {
		t.Fatalf("parse marginalia.go: %v", err)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}
		// The HTTP caller is the function whose body contains the
		// api.marginalia.nu URL string literal.
		var hasMarginaliaURL bool
		ast.Inspect(fn, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING && strings.Contains(lit.Value, "api.marginalia.nu") {
				hasMarginaliaURL = true
			}
			return true
		})
		if hasMarginaliaURL {
			if fn.Name.IsExported() {
				t.Errorf("function %s issues the Marginalia HTTP call but is exported; "+
					"must be unexported so runMarginalia is the sole guarded entry point", fn.Name.Name)
			}
			return
		}
	}
}
