package text

import "testing"

func TestCleanHTML(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"plain text", "hello world", "hello world"},
		{"strip tags", "<p>hello <b>world</b></p>", "hello world"},
		{"empty", "", ""},
		{"only tags", "<div><span></span></div>", ""},
		{"trim whitespace", "  <p> hello </p>  ", "hello"},
		{"self-closing", "line<br/>break", "linebreak"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanHTML(tt.input)
			if got != tt.want {
				t.Errorf("CleanHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCleanLines(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"normal", "a\nb\nc", "a\nb\nc"},
		{"empty lines", "a\n\n\nb", "a\nb"},
		{"whitespace lines", "a\n   \n  \nb", "a\nb"},
		{"trailing newline", "a\nb\n", "a\nb"},
		{"all empty", "\n\n\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanLines(tt.input)
			if got != tt.want {
				t.Errorf("CleanLines(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name, input string
		n           int
		want        string
	}{
		{"short", "abc", 10, "abc"},
		{"exact", "abc", 3, "abc"},
		{"truncated", "abcdef", 3, "abc"},
		{"empty", "", 5, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.input, tt.n)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
			}
		})
	}
}

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name, input string
		limit       int
		suffix      string
		want        string
	}{
		{"short", "hello", 10, "...", "hello"},
		{"exact", "hello", 5, "...", "hello"},
		{"truncated", "hello world", 5, "...", "hello..."},
		{"no suffix", "hello world", 5, "", "hello"},
		{"cyrillic", "Привет мир", 6, "…", "Привет…"},
		{"emoji", "Hello 🌍🌎🌏", 7, "...", "Hello 🌍..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateRunes(tt.input, tt.limit, tt.suffix)
			if got != tt.want {
				t.Errorf("TruncateRunes(%q, %d, %q) = %q, want %q", tt.input, tt.limit, tt.suffix, got, tt.want)
			}
		})
	}
}

func TestTruncateAtWord(t *testing.T) {
	tests := []struct {
		name, input string
		maxLen      int
		want        string
	}{
		{"short", "hello world", 20, "hello world"},
		{"at word", "hello beautiful world", 15, "hello beautiful..."},
		{"no good boundary", "abcdefghijklmnop", 5, "abcde..."},
		{"exact", "hello", 5, "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateAtWord(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("TruncateAtWord(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}
