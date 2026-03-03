package text

import (
	"strings"
	"testing"
)

func TestChunker_Interface(t *testing.T) {
	var _ Chunker = NewCharacterChunker(100, 20)
}

func TestCharacterChunker_ShortText(t *testing.T) {
	c := NewCharacterChunker(100, 20)
	chunks := c.Chunk("short text")
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	if chunks[0] != "short text" {
		t.Errorf("chunk = %q, want %q", chunks[0], "short text")
	}
}

func TestCharacterChunker_ExactSize(t *testing.T) {
	c := NewCharacterChunker(10, 0)
	chunks := c.Chunk("0123456789")
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
}

func TestCharacterChunker_SplitsAtWordBoundary(t *testing.T) {
	c := NewCharacterChunker(30, 0)
	input := "The quick brown fox jumps over the lazy dog near the river"
	chunks := c.Chunk(input)
	if len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks, got %d", len(chunks))
	}
	for i, ch := range chunks {
		if len(ch) > 0 && ch[len(ch)-1] == ' ' {
			t.Errorf("chunk[%d] ends with space: %q", i, ch)
		}
	}
	joined := strings.Join(chunks, " ")
	for _, word := range strings.Fields(input) {
		if !strings.Contains(joined, word) {
			t.Errorf("word %q missing from chunks", word)
		}
	}
}

func TestCharacterChunker_Overlap(t *testing.T) {
	c := NewCharacterChunker(20, 5)
	input := "aaa bbb ccc ddd eee fff ggg hhh"
	chunks := c.Chunk(input)
	if len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks, got %d", len(chunks))
	}
}

func TestCharacterChunker_Empty(t *testing.T) {
	c := NewCharacterChunker(100, 20)
	chunks := c.Chunk("")
	if len(chunks) != 0 {
		t.Errorf("chunks = %d, want 0 for empty input", len(chunks))
	}
}

func TestCharacterChunker_UTF8(t *testing.T) {
	c := NewCharacterChunker(20, 0)
	input := "Привет мир это тест юникода в чанкере"
	chunks := c.Chunk(input)
	if len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks for UTF-8, got %d", len(chunks))
	}
	for i, ch := range chunks {
		for _, r := range ch {
			if r == '\uFFFD' {
				t.Errorf("chunk[%d] contains replacement char: %q", i, ch)
			}
		}
	}
}

func TestCharacterChunker_SingleLongWord(t *testing.T) {
	c := NewCharacterChunker(10, 0)
	input := "abcdefghijklmnopqrstuvwxyz"
	chunks := c.Chunk(input)
	if len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks for long word, got %d", len(chunks))
	}
	joined := strings.Join(chunks, "")
	if joined != input {
		t.Errorf("reassembled = %q, want %q", joined, input)
	}
}

func TestCharacterChunker_NeedsChunking_Short(t *testing.T) {
	c := NewCharacterChunker(100, 20)
	if c.NeedsChunking("short text") {
		t.Error("short text should not need chunking")
	}
}

func TestCharacterChunker_NeedsChunking_Long(t *testing.T) {
	c := NewCharacterChunker(10, 0)
	if !c.NeedsChunking("this is definitely longer than ten runes") {
		t.Error("long text should need chunking")
	}
}

func TestCharacterChunker_NeedsChunking_ExactSize(t *testing.T) {
	c := NewCharacterChunker(10, 0)
	if c.NeedsChunking("0123456789") {
		t.Error("text exactly at chunkSize should not need chunking")
	}
}

func TestCharacterChunker_NeedsChunking_Empty(t *testing.T) {
	c := NewCharacterChunker(100, 0)
	if c.NeedsChunking("") {
		t.Error("empty text should not need chunking")
	}
}

func TestCharacterChunker_NeedsChunking_UTF8(t *testing.T) {
	c := NewCharacterChunker(5, 0)
	if !c.NeedsChunking("Привет") { // 6 Cyrillic runes > 5
		t.Error("6 Cyrillic runes should need chunking with chunkSize=5")
	}
}
