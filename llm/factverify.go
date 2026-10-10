package llm

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/anatolykoptev/go-engine/sources"
	kitmetrics "github.com/anatolykoptev/go-kit/metrics"
)

// minFactQuoteLen is the minimum normalized quote length in runes. Below ~20
// chars a substring check is nearly meaningless — generic fragments
// ("the report said", "in recent years") appear in unrelated text and would
// produce false verifies. ~20 runes is roughly one short clause in Russian
// or English.
const minFactQuoteLen = 20

// Metric counters for fact verification. Names follow the package convention
// (llm_*_total); the consumer's registry applies its own namespace prefix.
const (
	metricFactsVerifiedTotal = "llm_facts_verified_total"
	metricFactsHeldTotal     = "llm_facts_held_total"
)

// factNumberRe matches digit runs with internal . , or space separators.
// Space-separated groups must be exactly 3 digits and word-bounded, so
// "1 234 567" is one number but "3 5" and "2024 5678" stay separate.
// NBSP/thin-space variants reach this as plain spaces post-normalization.
var factNumberRe = regexp.MustCompile(`[0-9]+(?:[.,][0-9]+)*\b(?: [0-9]{3}\b)*(?:[.,][0-9]+)?`)

// parseStructuredOutput parses raw LLM output into StructuredOutput, then
// verifies every fact's quote against the text the model was shown for its
// cited sources. Malformed JSON falls back to extracting the answer field.
// Facts are annotated (Status/HeldReason), never dropped; the Answer prose
// is NOT gated.
func (c *Client) parseStructuredOutput(raw string, results []sources.Result, contents map[string]string) *StructuredOutput {
	var out StructuredOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		if answer := ExtractJSONAnswer(raw); answer != "" {
			return &StructuredOutput{Answer: answer}
		}
		return &StructuredOutput{Answer: raw}
	}
	c.verifyFacts(out.Facts, results, contents)
	return &out
}

// verifyFacts checks each fact's quote against the haystack the model was
// shown for each cited source: the FULL contents[url] value when fetched
// content exists, else the result snippet — mirroring
// BuildSourcesText/BuildSourcesTextWeighted, which send fetched content for
// top-ranked sources and only the snippet for the rest. A fact verifies when
// its quote is found in the haystack of ANY cited index. Facts are annotated
// in place, in order, and counted by outcome.
func (c *Client) verifyFacts(facts []FactItem, results []sources.Result, contents map[string]string) {
	if len(facts) == 0 {
		return
	}
	haystacks := make(map[int]string, len(results))
	for i, r := range results {
		h := contents[r.URL]
		if h == "" {
			h = r.Content
		}
		if h != "" {
			haystacks[i+1] = normalizeFactText(h)
		}
	}
	for i := range facts {
		status, reason := verifyFact(&facts[i], len(results), haystacks)
		facts[i].Status = status
		facts[i].HeldReason = reason
		if status == FactStatusVerified {
			c.metrics.Incr(metricFactsVerifiedTotal)
		} else {
			c.metrics.Incr(kitmetrics.Label(metricFactsHeldTotal, "reason", reason))
		}
	}
}

// verifyFact runs the hold checks in order and returns the fact's status.
// First failure wins: no_quote → bad_source_index → quote_too_short →
// quote_not_in_source → number_not_in_quote → verified.
func verifyFact(f *FactItem, numSources int, haystacks map[int]string) (string, string) {
	if strings.TrimSpace(f.Quote) == "" {
		return FactStatusHeld, HeldNoQuote
	}
	valid := false
	for _, idx := range f.Sources {
		if idx >= 1 && idx <= numSources {
			valid = true
			break
		}
	}
	if !valid {
		return FactStatusHeld, HeldBadSourceIndex
	}
	quote := normalizeFactText(f.Quote)
	if utf8.RuneCountInString(quote) < minFactQuoteLen {
		return FactStatusHeld, HeldQuoteTooShort
	}
	found := false
	for _, idx := range f.Sources {
		if h, ok := haystacks[idx]; ok && strings.Contains(h, quote) {
			found = true
			break
		}
	}
	if !found {
		return FactStatusHeld, HeldQuoteNotInSource
	}
	if !factNumbersCovered(f.Point, f.Quote) {
		return FactStatusHeld, HeldNumberNotInQuote
	}
	return FactStatusVerified, ""
}

// normalizeFactText canonicalizes a string for quote↔source comparison,
// applied identically to the LLM's quote and to the source haystack.
// Case-folds (unicode-aware lowercase), maps curly/low quotes and guillemets
// to ASCII ' and ", maps en/em dash and minus sign to -, maps ё→е (a common
// Russian spelling drift), deletes soft hyphens and zero-width chars, maps
// NBSP/narrow-NBSP/thin space to a plain space (strings.Fields already
// collapses every Unicode White_Space run), and trims.
func normalizeFactText(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(factNormalizeRune, s)
	return strings.Join(strings.Fields(s), " ")
}

// factNormalizeRune maps one rune during normalization. Returning -1 deletes.
func factNormalizeRune(r rune) rune {
	switch r {
	case '‘', '’', '‚', '‛':
		return '\''
	case '“', '”', '„', '‟', '«', '»':
		return '"'
	case '–', '—', '−': // en dash, em dash, minus sign
		return '-'
	case 'ё', 'Ё':
		return 'е'
	case '\u00ad', '\u200b', '\u200c', '\u200d', '\u2060', '\ufeff': // soft hyphen, zero-width chars
		return -1
	case '\u00a0', '\u202f', '\u2009': // NBSP, narrow NBSP, thin space
		return ' '
	}
	return r
}

// factNumbersCovered reports whether every number in point also appears in
// quote. Comparison is deliberately lenient on separator ambiguity (ru "3,5"
// decimal comma vs en "1,234" thousands): a number matches if ANY of its
// forms — digits-only or decimal — equals a quote number's form. A false
// hold is the failure we measure, so ambiguity resolves toward matching.
func factNumbersCovered(point, quote string) bool {
	need := factNumberRe.FindAllString(normalizeFactText(point), -1)
	if len(need) == 0 {
		return true
	}
	have := make(map[string]bool, len(need))
	for _, tok := range factNumberRe.FindAllString(normalizeFactText(quote), -1) {
		for _, form := range numberForms(tok) {
			have[form] = true
		}
	}
	for _, tok := range need {
		found := false
		for _, form := range numberForms(tok) {
			if have[form] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// numberForms returns the canonical forms a number token may represent:
// all digits concatenated (thousands separators stripped), plus — when the
// token contains a . or , separator — a decimal form keeping only the last
// separator as the decimal mark. "3,5" → {"35","3.5"}, "1,234" →
// {"1234","1.234"}, "1,234,567" → {"1234567","1234.567"}.
func numberForms(token string) []string {
	compact := strings.ReplaceAll(token, " ", "")
	forms := []string{stripNonDigits(compact)}
	last := strings.LastIndexAny(compact, ".,")
	if last <= 0 || last == len(compact)-1 {
		return forms
	}
	intPart := stripNonDigits(compact[:last])
	fracPart := compact[last+1:]
	if intPart != "" && fracPart != "" {
		forms = append(forms, intPart+"."+fracPart)
	}
	return forms
}

// stripNonDigits removes everything but ASCII digits.
func stripNonDigits(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
