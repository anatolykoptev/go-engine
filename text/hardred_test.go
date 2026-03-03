package text

import (
	"strings"
	"testing"
)

// --- Hard Red: CharacterChunker edge cases ---

func TestHR_Chunker_EmptyString(t *testing.T) {
	c := NewCharacterChunker(100, 10)
	result := c.Chunk("")
	if result != nil {
		t.Errorf("expected nil for empty string, got %v", result)
	}
}

func TestHR_Chunker_OnlyWhitespace(t *testing.T) {
	c := NewCharacterChunker(100, 10)
	result := c.Chunk("   \t\n  ")
	if result != nil {
		t.Errorf("expected nil for whitespace-only string, got %v", result)
	}
}

func TestHR_Chunker_SingleChar(t *testing.T) {
	c := NewCharacterChunker(100, 10)
	result := c.Chunk("x")
	if len(result) != 1 || result[0] != "x" {
		t.Errorf("single char: got %v", result)
	}
}

func TestHR_Chunker_SingleHugeWord(t *testing.T) {
	// No word boundaries — must force-split at chunkSize.
	c := NewCharacterChunker(10, 2)
	word := strings.Repeat("a", 50)
	result := c.Chunk(word)
	if len(result) == 0 {
		t.Fatal("expected chunks from huge word")
	}
	// Every chunk should be <= chunkSize runes.
	for i, ch := range result {
		if len([]rune(ch)) > 10 {
			t.Errorf("chunk %d exceeds chunkSize: %d runes", i, len([]rune(ch)))
		}
	}
}

func TestHR_Chunker_AllSpaces(t *testing.T) {
	c := NewCharacterChunker(10, 2)
	result := c.Chunk(strings.Repeat(" ", 100))
	if result != nil {
		t.Errorf("expected nil for all-spaces, got %d chunks", len(result))
	}
}

func TestHR_Chunker_UTF8_Emoji(t *testing.T) {
	c := NewCharacterChunker(5, 1)
	input := "Hello World"
	result := c.Chunk(input)
	if len(result) == 0 {
		t.Fatal("expected chunks")
	}
	// Reconstructed content should not corrupt runes.
	joined := strings.Join(result, " ")
	for _, r := range joined {
		if r == '\uFFFD' { // unicode replacement character
			t.Fatal("found replacement character — UTF-8 corruption")
		}
	}
}

func TestHR_Chunker_UTF8_CyrillicMultibyte(t *testing.T) {
	c := NewCharacterChunker(10, 2)
	// Cyrillic characters are 2 bytes each.
	input := "Привет мир это тест кириллицы"
	result := c.Chunk(input)
	if len(result) == 0 {
		t.Fatal("expected chunks for Cyrillic text")
	}
	for i, ch := range result {
		if len([]rune(ch)) > 10 {
			t.Errorf("chunk %d has %d runes, want <= 10", i, len([]rune(ch)))
		}
	}
}

func TestHR_Chunker_UTF8_ChineseCharacters(t *testing.T) {
	c := NewCharacterChunker(5, 1)
	// Chinese characters: 3 bytes each, no spaces between words.
	input := "这是一个测试字符串用于检查分块"
	result := c.Chunk(input)
	if len(result) == 0 {
		t.Fatal("expected chunks for Chinese text")
	}
	for i, ch := range result {
		if len([]rune(ch)) > 5 {
			t.Errorf("chunk %d has %d runes, want <= 5", i, len([]rune(ch)))
		}
	}
}

func TestHR_Chunker_OverlapEqualsChunkSize(t *testing.T) {
	// overlap >= chunkSize should be clamped to chunkSize/4.
	c := NewCharacterChunker(10, 10)
	input := strings.Repeat("word ", 20)
	result := c.Chunk(input)
	if len(result) == 0 {
		t.Fatal("expected chunks")
	}
	// Must make forward progress (not infinite loop).
}

func TestHR_Chunker_OverlapExceedsChunkSize(t *testing.T) {
	c := NewCharacterChunker(10, 100)
	input := strings.Repeat("word ", 20)
	result := c.Chunk(input)
	if len(result) == 0 {
		t.Fatal("expected chunks")
	}
}

func TestHR_Chunker_ZeroOverlap(t *testing.T) {
	c := NewCharacterChunker(10, 0)
	input := "one two three four five six seven eight"
	result := c.Chunk(input)
	if len(result) == 0 {
		t.Fatal("expected chunks")
	}
}

func TestHR_Chunker_ChunkSize1(t *testing.T) {
	c := NewCharacterChunker(1, 0)
	input := "abc"
	result := c.Chunk(input)
	// Each character should be its own chunk.
	if len(result) < 3 {
		t.Errorf("expected >= 3 chunks for 3-char input with chunkSize=1, got %d", len(result))
	}
}

// --- Hard Red: BM25Filter edge cases ---

func TestHR_BM25Filter_TopK0(t *testing.T) {
	f := NewBM25Filter(0)
	chunks := []string{"hello", "world"}
	result := f.Filter(chunks, "hello")
	if len(result) != 0 {
		t.Errorf("topK=0 should return 0 chunks, got %d", len(result))
	}
}

func TestHR_BM25Filter_SingleChunk(t *testing.T) {
	f := NewBM25Filter(5)
	chunks := []string{"the only chunk about golang"}
	result := f.Filter(chunks, "golang")
	if len(result) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(result))
	}
	if result[0] != chunks[0] {
		t.Errorf("wrong chunk returned")
	}
}

func TestHR_BM25Filter_AllIdenticalChunks(t *testing.T) {
	f := NewBM25Filter(3)
	chunks := make([]string, 10)
	for i := range chunks {
		chunks[i] = "identical content about golang"
	}
	result := f.Filter(chunks, "golang")
	if len(result) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(result))
	}
}

func TestHR_BM25Filter_QueryTermNotInAnyChunk(t *testing.T) {
	f := NewBM25Filter(3)
	chunks := []string{"hello world", "foo bar", "baz qux"}
	result := f.Filter(chunks, "nonexistent")
	// All scores should be 0, but should still return topK.
	if len(result) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(result))
	}
}

func TestHR_BM25Filter_EmptyChunks(t *testing.T) {
	f := NewBM25Filter(5)
	result := f.Filter(nil, "query")
	if result != nil {
		t.Errorf("expected nil for nil chunks, got %v", result)
	}
}

func TestHR_BM25Filter_EmptyQuery(t *testing.T) {
	f := NewBM25Filter(2)
	chunks := []string{"a", "b", "c"}
	result := f.Filter(chunks, "")
	if len(result) != 2 {
		t.Errorf("empty query with topK=2 should return 2 chunks, got %d", len(result))
	}
}

func TestHR_BM25Filter_VeryLongQuery(t *testing.T) {
	f := NewBM25Filter(3)
	chunks := []string{"go programming language", "python script", "rust systems"}
	query := strings.Repeat("go programming language systems ", 100)
	result := f.Filter(chunks, query)
	if len(result) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(result))
	}
}

func TestHR_BM25Filter_ChunksWithEmptyStrings(t *testing.T) {
	f := NewBM25Filter(3)
	chunks := []string{"", "hello world", "", "golang testing", ""}
	result := f.Filter(chunks, "golang")
	if len(result) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(result))
	}
}
