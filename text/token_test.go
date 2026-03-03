package text

import (
	"strings"
	"testing"
)

func TestEstimateTokens_English(t *testing.T) {
	// "hello world" = 11 chars. At 3.5 chars/token = ceil(11/3.5) = ceil(3.14) = 4
	got := EstimateTokens("hello world", DefaultCharsPerToken)
	if got != 4 {
		t.Errorf("EstimateTokens = %d, want 4", got)
	}
}

func TestEstimateTokens_Empty(t *testing.T) {
	got := EstimateTokens("", DefaultCharsPerToken)
	if got != 0 {
		t.Errorf("EstimateTokens empty = %d, want 0", got)
	}
}

func TestEstimateTokens_CustomRate(t *testing.T) {
	// 10 chars at 2.5 chars/token = ceil(4.0) = 4
	got := EstimateTokens("0123456789", 2.5)
	if got != 4 {
		t.Errorf("EstimateTokens = %d, want 4", got)
	}
}

func TestTruncateToTokenBudget_NoTruncation(t *testing.T) {
	text := "short"
	got := TruncateToTokenBudget(text, 100, DefaultCharsPerToken)
	if got != text {
		t.Errorf("got %q, want %q (no truncation needed)", got, text)
	}
}

func TestTruncateToTokenBudget_Truncates(t *testing.T) {
	text := strings.Repeat("a", 1000)
	// Budget: 100 tokens at 3.5 chars/token = 350 chars max.
	got := TruncateToTokenBudget(text, 100, DefaultCharsPerToken)
	if len(got) > 350 {
		t.Errorf("len = %d, want <= 350", len(got))
	}
	if len(got) == 0 {
		t.Error("should not be empty")
	}
}

func TestTruncateToTokenBudget_Empty(t *testing.T) {
	got := TruncateToTokenBudget("", 100, DefaultCharsPerToken)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestTruncateToTokenBudget_ZeroBudget(t *testing.T) {
	got := TruncateToTokenBudget("hello", 0, DefaultCharsPerToken)
	if got != "" {
		t.Errorf("got %q, want empty for zero budget", got)
	}
}

func TestTruncateToTokenBudget_UTF8Safe(t *testing.T) {
	// Cyrillic: each rune is 2 bytes. Should not split mid-rune.
	text := strings.Repeat("П", 200) // 200 runes, 400 bytes
	got := TruncateToTokenBudget(text, 10, 2.5)
	// Budget: 10 tokens * 2.5 = 25 bytes max
	for _, r := range got {
		if r == '\uFFFD' {
			t.Error("truncation split a multi-byte rune")
		}
	}
	if len(got) > 25 {
		t.Errorf("byte len = %d, want <= 25", len(got))
	}
}
