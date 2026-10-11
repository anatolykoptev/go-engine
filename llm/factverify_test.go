package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	engmetrics "github.com/anatolykoptev/go-engine/metrics"
	"github.com/anatolykoptev/go-engine/sources"
)

// summarizeCall invokes one of the public Summarize* entry points.
type summarizeCall func(ctx context.Context, c *Client, results []sources.Result, contents map[string]string) (*StructuredOutput, error)

// summarizeEntryPoints covers every public Summarize* method that must route
// through the shared parse-and-verify path.
var summarizeEntryPoints = []struct {
	name string
	call summarizeCall
}{
	{"Summarize", func(ctx context.Context, c *Client, r []sources.Result, ct map[string]string) (*StructuredOutput, error) {
		return c.Summarize(ctx, "test query", 2000, 4.0, r, ct)
	}},
	{"SummarizeWithInstruction", func(ctx context.Context, c *Client, r []sources.Result, ct map[string]string) (*StructuredOutput, error) {
		return c.SummarizeWithInstruction(ctx, "test query", "instruction", 2000, 4.0, r, ct)
	}},
	{"SummarizeDeep", func(ctx context.Context, c *Client, r []sources.Result, ct map[string]string) (*StructuredOutput, error) {
		return c.SummarizeDeep(ctx, "test query", "instruction", 2000, 4.0, r, ct)
	}},
	{"SummarizeWithOpts", func(ctx context.Context, c *Client, r []sources.Result, ct map[string]string) (*StructuredOutput, error) {
		return c.SummarizeWithOpts(ctx, SummarizeOpts{Query: "test query", TotalBudget: 2000, CharsPerToken: 4.0}, r, ct)
	}},
	{"SummarizeDeepWithOpts", func(ctx context.Context, c *Client, r []sources.Result, ct map[string]string) (*StructuredOutput, error) {
		return c.SummarizeDeepWithOpts(ctx, SummarizeOpts{Query: "test query", Instruction: "instruction", TotalBudget: 2000, CharsPerToken: 4.0}, r, ct)
	}},
	{"SummarizeWithTier_base", func(ctx context.Context, c *Client, r []sources.Result, ct map[string]string) (*StructuredOutput, error) {
		return c.SummarizeWithTier(ctx, SummarizeOpts{Query: "test query", TotalBudget: 2000, CharsPerToken: 4.0}, r, ct, rankedWeights, false)
	}},
	{"SummarizeWithTier_deep", func(ctx context.Context, c *Client, r []sources.Result, ct map[string]string) (*StructuredOutput, error) {
		return c.SummarizeWithTier(ctx, SummarizeOpts{Query: "test query", TotalBudget: 2000, CharsPerToken: 4.0}, r, ct, rankedWeights, true)
	}},
}

func newTestClient(t *testing.T, response string) (*Client, func()) {
	t.Helper()
	srv := mockLLMServer(t, response)
	return New(WithAPIBase(srv.URL), WithAPIKey("k"), WithModel("m")), srv.Close
}

// TestFacts_HeldWhenQuoteNotInSource is the F1 journey: an LLM-invented quote
// must surface as held/quote_not_in_source through EVERY public entry point.
func TestFacts_HeldWhenQuoteNotInSource(t *testing.T) {
	const resp = `{"answer":"A.","facts":[{"point":"The committee approved the plan.","sources":[1],"quote":"a fabricated span that never appeared anywhere"}]}`
	for _, ep := range summarizeEntryPoints {
		t.Run(ep.name, func(t *testing.T) {
			c, done := newTestClient(t, resp)
			defer done()
			results := []sources.Result{{Title: "T", URL: "http://a.com", Content: "The committee met on Tuesday to discuss."}}
			out, err := ep.call(context.Background(), c, results, nil)
			if err != nil {
				t.Fatalf("%s: %v", ep.name, err)
			}
			if len(out.Facts) != 1 {
				t.Fatalf("%s: facts = %d, want 1", ep.name, len(out.Facts))
			}
			f := out.Facts[0]
			if f.Status != FactStatusHeld || f.HeldReason != HeldQuoteNotInSource {
				t.Errorf("%s: status=%q reason=%q, want %s/%s",
					ep.name, f.Status, f.HeldReason, FactStatusHeld, HeldQuoteNotInSource)
			}
			if f.Point != "The committee approved the plan." {
				t.Errorf("%s: held fact mutated or dropped: %+v", ep.name, f)
			}
		})
	}
}

// TestFacts_VerifiedWhenQuoteMatches covers the happy path for every entry
// point: quote verbatim in the fetched content, all numbers covered.
func TestFacts_VerifiedWhenQuoteMatches(t *testing.T) {
	const resp = `{"answer":"A.","facts":[{"point":"Revenue grew 15% in 2024.","sources":[1],"quote":"revenue grew 15% in 2024 according to the filing"}]}`
	for _, ep := range summarizeEntryPoints {
		t.Run(ep.name, func(t *testing.T) {
			c, done := newTestClient(t, resp)
			defer done()
			results := []sources.Result{{Title: "T", URL: "http://a.com", Content: "ignored snippet"}}
			contents := map[string]string{
				"http://a.com": "Acme said revenue grew 15% in 2024 according to the filing published Monday.",
			}
			out, err := ep.call(context.Background(), c, results, contents)
			if err != nil {
				t.Fatalf("%s: %v", ep.name, err)
			}
			if len(out.Facts) != 1 {
				t.Fatalf("%s: facts = %d, want 1", ep.name, len(out.Facts))
			}
			f := out.Facts[0]
			if f.Status != FactStatusVerified || f.HeldReason != "" {
				t.Errorf("%s: status=%q reason=%q, want %s/<empty>",
					ep.name, f.Status, f.HeldReason, FactStatusVerified)
			}
		})
	}
}

// TestFacts_HeldWhenNumberMissingFromQuote is the F2 journey on the go-search
// research path (SummarizeWithTier): the quote is real but does not carry the
// number the point claims.
func TestFacts_HeldWhenNumberMissingFromQuote(t *testing.T) {
	const resp = `{"answer":"A.","facts":[{"point":"Revenue jumped 24.4 percent.","sources":[1],"quote":"revenue grew significantly year over year"}]}`
	c, done := newTestClient(t, resp)
	defer done()
	results := []sources.Result{{Title: "T", URL: "http://a.com"}}
	contents := map[string]string{
		"http://a.com": "Acme reported revenue grew significantly year over year, jumping 24.4% in Q3.",
	}
	out, err := c.SummarizeWithTier(context.Background(),
		SummarizeOpts{Query: "q", TotalBudget: 2000, CharsPerToken: 4.0},
		results, contents, rankedWeights, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Facts) != 1 {
		t.Fatalf("facts = %d, want 1", len(out.Facts))
	}
	f := out.Facts[0]
	if f.Status != FactStatusHeld || f.HeldReason != HeldNumberNotInQuote {
		t.Errorf("status=%q reason=%q, want %s/%s", f.Status, f.HeldReason, FactStatusHeld, HeldNumberNotInQuote)
	}
}

// TestFacts_RussianNormalization is the F3 journey: source text with
// guillemets, ё and NBSP verifies against a quote using straight quotes,
// е and a plain space.
func TestFacts_RussianNormalization(t *testing.T) {
	const resp = `{"answer":"A.","facts":[{"point":"Продажи выросли на 15% за год.","sources":[1],"quote":"По данным \"Интерфакса\", объем продаж вырос на 15% за год"}]}`
	c, done := newTestClient(t, resp)
	defer done()
	results := []sources.Result{{Title: "T", URL: "http://a.com"}}
	contents := map[string]string{
		"http://a.com": "По данным «Интерфакса», объём продаж вырос на 15% за год, сообщает агентство.",
	}
	out, err := c.SummarizeWithTier(context.Background(),
		SummarizeOpts{Query: "q", TotalBudget: 2000, CharsPerToken: 4.0},
		results, contents, rankedWeights, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Facts) != 1 {
		t.Fatalf("facts = %d, want 1", len(out.Facts))
	}
	if out.Facts[0].Status != FactStatusVerified {
		t.Errorf("status=%q reason=%q, want %s", out.Facts[0].Status, out.Facts[0].HeldReason, FactStatusVerified)
	}
}

// TestFacts_SnippetOnlySource is the F4 journey: a source with no fetched
// content verifies against the results[i].Content snippet.
func TestFacts_SnippetOnlySource(t *testing.T) {
	const resp = `{"answer":"A.","facts":[{"point":"The library reached version 2.0.","sources":[1],"quote":"the library reached version 2.0 in march"}]}`
	c, done := newTestClient(t, resp)
	defer done()
	results := []sources.Result{{
		Title:   "T",
		URL:     "http://a.com",
		Content: "The library reached version 2.0 in March, the maintainers announced.",
	}}
	out, err := c.SummarizeWithTier(context.Background(),
		SummarizeOpts{Query: "q", TotalBudget: 2000, CharsPerToken: 4.0},
		results, nil, rankedWeights, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Facts) != 1 {
		t.Fatalf("facts = %d, want 1", len(out.Facts))
	}
	if out.Facts[0].Status != FactStatusVerified {
		t.Errorf("status=%q reason=%q, want %s", out.Facts[0].Status, out.Facts[0].HeldReason, FactStatusVerified)
	}
}

// TestFacts_BadSourceIndex is the F5 journey: out-of-range indices are held,
// never a panic; a mix of invalid and valid indices still verifies.
func TestFacts_BadSourceIndex(t *testing.T) {
	results := []sources.Result{
		{Title: "T1", URL: "http://a.com", Content: "The committee met on Tuesday to discuss."},
		{Title: "T2", URL: "http://b.com", Content: "Rainfall doubled in the region."},
	}
	cases := []struct {
		name       string
		fact       string
		wantReason string
		wantStatus string
	}{
		{"index_zero", `{"point":"P.","sources":[0],"quote":"The committee met on Tuesday to discuss"}`, HeldBadSourceIndex, FactStatusHeld},
		{"index_over_len", `{"point":"P.","sources":[3],"quote":"The committee met on Tuesday to discuss"}`, HeldBadSourceIndex, FactStatusHeld},
		{"no_sources", `{"point":"P.","sources":[],"quote":"The committee met on Tuesday to discuss"}`, HeldBadSourceIndex, FactStatusHeld},
		{"mixed_invalid_and_valid", `{"point":"P.","sources":[0,2],"quote":"rainfall doubled in the region"}`, "", FactStatusVerified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := `{"answer":"A.","facts":[` + tc.fact + `]}`
			c, done := newTestClient(t, resp)
			defer done()
			out, err := c.SummarizeWithTier(context.Background(),
				SummarizeOpts{Query: "q", TotalBudget: 2000, CharsPerToken: 4.0},
				results, nil, rankedWeights, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Facts) != 1 {
				t.Fatalf("facts = %d, want 1", len(out.Facts))
			}
			f := out.Facts[0]
			if f.Status != tc.wantStatus || f.HeldReason != tc.wantReason {
				t.Errorf("status=%q reason=%q, want %s/%q", f.Status, f.HeldReason, tc.wantStatus, tc.wantReason)
			}
		})
	}
}

// TestFacts_HeldReasons covers the remaining hold reasons in check order.
func TestFacts_HeldReasons(t *testing.T) {
	results := []sources.Result{{Title: "T", URL: "http://a.com", Content: "The committee met on Tuesday to discuss."}}
	cases := []struct {
		name       string
		fact       string
		wantReason string
	}{
		{"no_quote_field", `{"point":"P.","sources":[1]}`, HeldNoQuote},
		{"empty_quote", `{"point":"P.","sources":[1],"quote":"  "}`, HeldNoQuote},
		{"quote_too_short", `{"point":"P.","sources":[1],"quote":"committee met"}`, HeldQuoteTooShort},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := `{"answer":"A.","facts":[` + tc.fact + `]}`
			c, done := newTestClient(t, resp)
			defer done()
			out, err := c.SummarizeWithTier(context.Background(),
				SummarizeOpts{Query: "q", TotalBudget: 2000, CharsPerToken: 4.0},
				results, nil, rankedWeights, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Facts) != 1 {
				t.Fatalf("facts = %d, want 1", len(out.Facts))
			}
			f := out.Facts[0]
			if f.Status != FactStatusHeld || f.HeldReason != tc.wantReason {
				t.Errorf("status=%q reason=%q, want %s/%s", f.Status, f.HeldReason, FactStatusHeld, tc.wantReason)
			}
		})
	}
}

// TestFacts_NumberLeniency covers the ambiguous-separator cases: ru decimal
// comma vs en decimal point, and grouped thousands.
func TestFacts_NumberLeniency(t *testing.T) {
	results := []sources.Result{{Title: "T", URL: "http://a.com"}}
	contents := map[string]string{
		"http://a.com": "Sales grew 3.5 times to reach 1,234 units in the reported period.",
	}
	cases := []struct {
		name string
		fact string
		want string
	}{
		{"ru_decimal_comma", `{"point":"Продажи выросли в 3,5 раза.","sources":[1],"quote":"sales grew 3.5 times"}`, FactStatusVerified},
		{"grouped_thousands", `{"point":"Sales reached 1234 units.","sources":[1],"quote":"reach 1,234 units in the reported period"}`, FactStatusVerified},
		{"different_number", `{"point":"Sales reached 5678 units.","sources":[1],"quote":"reach 1,234 units in the reported period"}`, FactStatusHeld},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := `{"answer":"A.","facts":[` + tc.fact + `]}`
			c, done := newTestClient(t, resp)
			defer done()
			out, err := c.SummarizeWithTier(context.Background(),
				SummarizeOpts{Query: "q", TotalBudget: 2000, CharsPerToken: 4.0},
				results, contents, rankedWeights, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Facts) != 1 {
				t.Fatalf("facts = %d, want 1", len(out.Facts))
			}
			if out.Facts[0].Status != tc.want {
				t.Errorf("status=%q reason=%q, want %s", out.Facts[0].Status, out.Facts[0].HeldReason, tc.want)
			}
		})
	}
}

// factResponse marshals a single-fact LLM response for the given point and
// quote, so test tables can carry Unicode escapes safely.
func factResponse(t *testing.T, point, quote string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"answer": "A.",
		"facts":  []map[string]any{{"point": point, "sources": []int{1}, "quote": quote}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestFacts_NumberRuleDistinctValues: the number rule must HOLD — not
// verify — when the point's number is a different value than every number
// the quote carries. A bare digit-concatenation would equate each pair;
// a false verify is the silent failure this rule exists to catch.
func TestFacts_NumberRuleDistinctValues(t *testing.T) {
	cases := []struct {
		name   string
		point  string
		quote  string
		source string
	}{
		{"decimal_vs_integer", "Inflation reached 1.5 percent.",
			"inflation reached 15 percent last quarter",
			"The report said inflation reached 15 percent last quarter, officials confirmed."},
		{"negative_vs_positive", "Temperatures fell to -15 degrees.",
			"temperatures rose to 15 degrees this winter",
			"Forecasters said temperatures rose to 15 degrees this winter in the region."},
		{"integer_vs_version", "Downloads hit 1234 units.",
			"the package shipped version 1.2.3.4 for users",
			"Maintainers said the package shipped version 1.2.3.4 for users on Monday."},
		{"integer_vs_decimal_comma", "The fee is 12 euros.",
			"the fee is 1,2 euros per transaction",
			"The bank said the fee is 1,2 euros per transaction for clients."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, done := newTestClient(t, factResponse(t, tc.point, tc.quote))
			defer done()
			results := []sources.Result{{Title: "T", URL: "http://a.com"}}
			contents := map[string]string{"http://a.com": tc.source}
			out, err := c.SummarizeWithTier(context.Background(),
				SummarizeOpts{Query: "q", TotalBudget: 2000, CharsPerToken: 4.0},
				results, contents, rankedWeights, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Facts) != 1 {
				t.Fatalf("facts = %d, want 1", len(out.Facts))
			}
			f := out.Facts[0]
			if f.Status != FactStatusHeld || f.HeldReason != HeldNumberNotInQuote {
				t.Errorf("status=%q reason=%q, want %s/%s", f.Status, f.HeldReason, FactStatusHeld, HeldNumberNotInQuote)
			}
		})
	}
}

// TestFacts_NumberRuleValueEquivalence: the same number written with
// different separators must still verify — ru decimal comma, en/de/ru
// thousands groupings, Unicode minus — plus non-ASCII digits (Nd → ASCII)
// and decomposed Unicode (NFC). Ambiguity resolves toward matching only
// between equal values.
func TestFacts_NumberRuleValueEquivalence(t *testing.T) {
	cases := []struct {
		name   string
		point  string
		quote  string
		source string
	}{
		{"ru_decimal_comma", "Sales grew 3,5 times.",
			"sales grew 3.5 times in q3",
			"The filing said sales grew 3.5 times in Q3, executives noted."},
		{"en_thousands", "Sales reached 1234 units.",
			"sales reached 1,234 units this quarter",
			"The filing said sales reached 1,234 units this quarter overall."},
		{"space_thousands", "Sales reached 1234 units.",
			"sales reached 1 234 units this quarter",
			"The filing said sales reached 1 234 units this quarter overall."},
		{"nbsp_thousands", "Sales reached 1234 units.",
			"sales reached 1 234 units this quarter",
			"The filing said sales reached 1 234 units this quarter overall."},
		{"space_millions", "The fund reached 1 500 000 euros.",
			"the fund reached 1500000 euros last year",
			"The audit said the fund reached 1500000 euros last year in total."},
		{"ambiguous_dot_vs_comma", "The index hit 1.234 points.",
			"the index hit 1,234 points yesterday",
			"The analyst said the index hit 1,234 points yesterday at close."},
		{"de_dot_thousands", "Sales hit 1234 units.",
			"the counter showed 1.234 on the display today",
			"Technicians said the counter showed 1.234 on the display today at noon."},
		{"unicode_minus", "Temperatures fell to -15 degrees.",
			"temperatures fell to −15 degrees this winter",
			"Forecasters said temperatures fell to −15 degrees this winter in the region."},
		{"fullwidth_digit", "Sales grew ３.5 times.",
			"sales grew 3.5 times in q3",
			"The filing said sales grew 3.5 times in Q3, executives noted."},
		{"arabic_indic_digit", "Sales grew ٣.5 times.",
			"sales grew 3.5 times in q3",
			"The filing said sales grew 3.5 times in Q3, executives noted."},
		{"decomposed_yo", "Объём продаж вырос на 15%.",
			"объём продаж вырос на 15% за год",
			"По данным агентства, объём продаж вырос на 15% за год, сообщается."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, done := newTestClient(t, factResponse(t, tc.point, tc.quote))
			defer done()
			results := []sources.Result{{Title: "T", URL: "http://a.com"}}
			contents := map[string]string{"http://a.com": tc.source}
			out, err := c.SummarizeWithTier(context.Background(),
				SummarizeOpts{Query: "q", TotalBudget: 2000, CharsPerToken: 4.0},
				results, contents, rankedWeights, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Facts) != 1 {
				t.Fatalf("facts = %d, want 1", len(out.Facts))
			}
			f := out.Facts[0]
			if f.Status != FactStatusVerified || f.HeldReason != "" {
				t.Errorf("status=%q reason=%q, want %s/<empty>", f.Status, f.HeldReason, FactStatusVerified)
			}
		})
	}
}

// TestFacts_PromptRequestsQuote is the F6 journey: every Summarize* method's
// prompt must ask the model for a quote.
func TestFacts_PromptRequestsQuote(t *testing.T) {
	for _, ep := range summarizeEntryPoints {
		t.Run(ep.name, func(t *testing.T) {
			var captured strings.Builder
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				for _, m := range req.Messages {
					captured.WriteString(m.Content)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(mockResponse{
					Choices: []mockChoice{{Message: mockMessage{Content: `{"answer":"ok"}`}}},
				})
			}))
			defer srv.Close()

			c := New(WithAPIBase(srv.URL), WithAPIKey("k"), WithModel("m"))
			results := []sources.Result{{Title: "T", URL: "http://a.com", Content: "snippet"}}
			if _, err := ep.call(context.Background(), c, results, nil); err != nil {
				t.Fatalf("%s: %v", ep.name, err)
			}
			// Both facts in the JSON example must show the quote key; the
			// PromptDeep code-example adds a third, so the floor is 2.
			if n := strings.Count(captured.String(), `"quote":`); n < 2 {
				t.Errorf(`%s: prompt JSON example has %d "quote" keys, want >= 2`, ep.name, n)
			}
			if !strings.Contains(captured.String(), "character-for-character") {
				t.Errorf("%s: prompt does not require verbatim quoting", ep.name)
			}
		})
	}
}

// TestFactItem_UncheckedStatusContract is the F7 journey: a FactItem built
// outside the verify path keeps Status == "" and marshals without the
// status/held_reason keys (go-search's extractive path relies on this).
func TestFactItem_UncheckedStatusContract(t *testing.T) {
	f := FactItem{Point: "p", Sources: []int{1}}
	if f.Status != "" || f.HeldReason != "" || f.Quote != "" {
		t.Fatalf("zero-value FactItem should be unchecked, got %+v", f)
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"point":"p","sources":[1]}`; got != want {
		t.Errorf("marshal = %s, want %s", got, want)
	}
}

// TestFacts_Metrics verifies verified and held facts are counted by reason.
func TestFacts_Metrics(t *testing.T) {
	const resp = `{"answer":"A.","facts":[
		{"point":"Revenue grew 15% in 2024.","sources":[1],"quote":"revenue grew 15% in 2024 according to the filing"},
		{"point":"Missing quote fact.","sources":[1]}
	]}`
	srv := mockLLMServer(t, resp)
	defer srv.Close()
	reg := engmetrics.New()
	c := New(WithAPIBase(srv.URL), WithAPIKey("k"), WithModel("m"), WithMetrics(reg))
	results := []sources.Result{{Title: "T", URL: "http://a.com"}}
	contents := map[string]string{
		"http://a.com": "Acme said revenue grew 15% in 2024 according to the filing published Monday.",
	}
	out, err := c.SummarizeWithTier(context.Background(),
		SummarizeOpts{Query: "q", TotalBudget: 2000, CharsPerToken: 4.0},
		results, contents, rankedWeights, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Facts) != 2 {
		t.Fatalf("facts = %d, want 2", len(out.Facts))
	}
	if got := reg.Value("llm_facts_verified_total"); got != 1 {
		t.Errorf("llm_facts_verified_total = %d, want 1", got)
	}
	held := reg.Snapshot()
	found := false
	for name, v := range held {
		if strings.HasPrefix(name, "llm_facts_held_total") && strings.Contains(name, "no_quote") && v == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected llm_facts_held_total{reason=no_quote}=1, snapshot: %v", held)
	}
}

// TestFacts_NilMetricsSafe ensures a nil registry never panics.
func TestFacts_NilMetricsSafe(t *testing.T) {
	const resp = `{"answer":"A.","facts":[{"point":"P.","sources":[1]}]}`
	c, done := newTestClient(t, resp)
	defer done()
	results := []sources.Result{{Title: "T", URL: "http://a.com", Content: "snippet"}}
	out, err := c.SummarizeWithTier(context.Background(),
		SummarizeOpts{Query: "q", TotalBudget: 2000, CharsPerToken: 4.0},
		results, nil, rankedWeights, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Facts) != 1 || out.Facts[0].Status != FactStatusHeld {
		t.Errorf("unexpected facts: %+v", out.Facts)
	}
}
