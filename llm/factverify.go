package llm

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/anatolykoptev/go-engine/sources"
	kitmetrics "github.com/anatolykoptev/go-kit/metrics"
	"golang.org/x/text/unicode/norm"
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
// A leading minus is attached by factNumberTokens, not by the regexp.
var factNumberRe = regexp.MustCompile(`[0-9]+(?:[.,][0-9]+)*\b(?: [0-9]{3}\b)*(?:[.,][0-9]+)?`)

// thousandsGroupLen is the digit count a separator group must have to read
// as a thousands separator (en "1,234", de/ru "1.234", ru "1 234").
const thousandsGroupLen = 3

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

// verifyFacts checks each fact's quote against the text of each cited
// source: the FULL contents[url] value when fetched content exists, else
// the result snippet. This mirrors BuildSourcesText/BuildSourcesTextWeighted
// only in the CHOICE of source text — fetched content for a source, else
// its snippet — not in token truncation: the haystack is the full text
// whether or not it fit the model's prompt budget. That is deliberate: a
// verbatim quote that exists in the source supports the fact regardless of
// whether the model was shown that part. A fact verifies when its quote is
// found in the haystack of ANY cited index. Facts are annotated in place,
// in order, and counted by outcome.
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
	s = norm.NFC.String(s)
	s = strings.ToLower(s)
	s = strings.Map(factNormalizeRune, s)
	return strings.Join(strings.Fields(s), " ")
}

// factNormalizeRune maps one rune during normalization. Returning -1 deletes.
// A Unicode decimal digit (category Nd — Arabic-Indic, full-width, …) maps
// to its ASCII equivalent so the number rule sees "3" for "٣" and "３".
func factNormalizeRune(r rune) rune {
	if r > '9' && unicode.IsDigit(r) {
		return '0' + ndDigitValue(r)
	}
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

// ndDigitValue returns the numeric value of a Unicode decimal digit. Nd
// digits are always encoded as ten contiguous runes 0-9, so the value is
// the count of contiguous digit runes immediately preceding it, modulo 10
// (an adjacent block's digits contribute whole tens — e.g. the five
// mathematical digit blocks U+1D7CE–U+1D7FF). Call only when
// unicode.IsDigit(r) holds — that is the membership test.
func ndDigitValue(r rune) rune {
	v := rune(0)
	for unicode.IsDigit(r - v - 1) {
		v++
	}
	return v % 10
}

// factNumbersCovered reports whether every number in point also appears in
// quote. Numbers compare by canonical VALUE (see numberForms): thousands
// groupings are dropped, a . or , may read as a decimal mark, and a leading
// minus is part of the value — so "1,234" equals "1234" but "1.5" never
// equals "15" and "-15" never equals "15". Version-like tokens
// ("1.2.3.4") match only their literal form. A false hold is the failure
// we measure; a false verify is silent, so ambiguity resolves toward
// matching only between equal values.
func factNumbersCovered(point, quote string) bool {
	need := factNumberTokens(normalizeFactText(point))
	if len(need) == 0 {
		return true
	}
	have := make(map[string]bool, len(need))
	for _, tok := range factNumberTokens(normalizeFactText(quote)) {
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

// factNumberTokens extracts number tokens from normalized text. A "-"
// directly before a token is its sign unless another digit precedes it:
// "15-20" is a range (15 and 20), "to -15" is negative fifteen.
func factNumberTokens(s string) []string {
	matches := factNumberRe.FindAllStringIndex(s, -1)
	toks := make([]string, 0, len(matches))
	for _, m := range matches {
		tok := s[m[0]:m[1]]
		if m[0] > 0 && s[m[0]-1] == '-' && (m[0] == 1 || !isDigitByte(s[m[0]-2])) {
			tok = "-" + tok
		}
		toks = append(toks, tok)
	}
	return toks
}

func isDigitByte(b byte) bool { return b >= '0' && b <= '9' }

// numberForms returns the canonical VALUES a number token may represent —
// never a bare digit-concatenation. A separator followed by exactly
// thousandsGroupLen digits may be a thousands separator (dropped in that
// reading); the last . or , may read as a decimal mark (kept as "." in
// that reading) when every earlier group is a valid thousands group. A
// token that fits neither reading ("1.2.3.4", "v1.55.0") is a
// version/identifier and yields only its literal form, which never equals
// a plain number. Examples: "1,234" → {"1234","1.234"}, "1.5" → {"1.5"},
// "1.2.3.4" → {"1.2.3.4"}, "-15" → {"-15"}.
func numberForms(token string) []string {
	sign, body := splitNumberSign(token)
	groups, seps := splitNumberToken(body)
	n := len(groups)
	var forms []string
	if n > 1 && allThreeDigit(groups[1:]) {
		forms = append(forms, canonicalNumber(sign+strings.Join(groups, "")))
	}
	if n > 1 && seps[n-2] != ' ' && allThreeDigit(groups[1:n-1]) {
		forms = append(forms, canonicalNumber(sign+strings.Join(groups[:n-1], "")+"."+groups[n-1]))
	}
	if forms == nil {
		if n == 1 {
			forms = []string{canonicalNumber(token)}
		} else {
			forms = []string{token} // version/identifier: literal form only
		}
	}
	return forms
}

// splitNumberSign separates a token's leading minus from its body.
func splitNumberSign(token string) (sign, body string) {
	if strings.HasPrefix(token, "-") {
		return "-", token[1:]
	}
	return "", token
}

// splitNumberToken splits a number token's body into its digit groups and
// the separators between them: "1,234.56" → ["1","234","56"], [',','.'].
func splitNumberToken(body string) (groups []string, seps []byte) {
	start := 0
	for i := 0; i < len(body); i++ {
		if body[i] == '.' || body[i] == ',' || body[i] == ' ' {
			groups = append(groups, body[start:i])
			seps = append(seps, body[i])
			start = i + 1
		}
	}
	return append(groups, body[start:]), seps
}

// allThreeDigit reports whether every group has exactly thousandsGroupLen
// digits, as a thousands separator requires.
func allThreeDigit(groups []string) bool {
	for _, g := range groups {
		if len(g) != thousandsGroupLen {
			return false
		}
	}
	return true
}

// canonicalNumber renders a numeric reading in canonical form so equal
// VALUES compare equal as strings: leading zeros stripped from the integer
// part, trailing zeros from the fraction, and no negative zero.
func canonicalNumber(v string) string {
	neg := strings.HasPrefix(v, "-")
	if neg {
		v = v[1:]
	}
	intPart, frac, hasFrac := strings.Cut(v, ".")
	intPart = strings.TrimLeft(intPart, "0")
	if intPart == "" {
		intPart = "0"
	}
	if hasFrac {
		frac = strings.TrimRight(frac, "0")
		hasFrac = frac != ""
	}
	out := intPart
	if hasFrac {
		out += "." + frac
	}
	if neg && out != "0" {
		return "-" + out
	}
	return out
}
