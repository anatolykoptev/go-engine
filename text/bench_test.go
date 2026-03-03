package text

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkCharacterChunker_1KB(b *testing.B) {
	c := NewCharacterChunker(200, 20)
	input := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 25) // ~1.1KB
	b.ResetTimer()
	for b.Loop() {
		c.Chunk(input)
	}
}

func BenchmarkCharacterChunker_100KB(b *testing.B) {
	c := NewCharacterChunker(500, 50)
	input := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 2500) // ~110KB
	b.ResetTimer()
	for b.Loop() {
		c.Chunk(input)
	}
}

func BenchmarkBM25Filter_50Chunks(b *testing.B) {
	f := NewBM25Filter(5)
	chunks := make([]string, 50)
	for i := range chunks {
		chunks[i] = fmt.Sprintf("Chunk %d about golang programming concurrency patterns testing and benchmarks", i)
	}
	b.ResetTimer()
	for b.Loop() {
		f.Filter(chunks, "golang concurrency testing")
	}
}

func BenchmarkBM25Filter_200Chunks(b *testing.B) {
	f := NewBM25Filter(10)
	chunks := make([]string, 200)
	for i := range chunks {
		chunks[i] = fmt.Sprintf("Document %d discussing various programming languages frameworks and tools for building web applications", i)
	}
	b.ResetTimer()
	for b.Loop() {
		f.Filter(chunks, "golang web framework performance")
	}
}
