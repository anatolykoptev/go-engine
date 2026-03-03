package extract

import (
	"net/url"
	"testing"
)

var benchHTML = []byte(sampleHTML)

func BenchmarkExtract_Trafilatura(b *testing.B) {
	ext := New()
	pageURL, _ := url.Parse("https://example.com/article")
	b.ResetTimer()
	for b.Loop() {
		_, _ = ext.extractTrafilatura(benchHTML, pageURL)
	}
}

func BenchmarkExtract_Goquery(b *testing.B) {
	ext := New()
	b.ResetTimer()
	for b.Loop() {
		_, _ = ext.extractGoquery(benchHTML)
	}
}

func BenchmarkExtract_Regex(b *testing.B) {
	ext := New()
	b.ResetTimer()
	for b.Loop() {
		_, _ = ext.extractRegex(benchHTML)
	}
}

func BenchmarkExtract_Full(b *testing.B) {
	ext := New()
	pageURL, _ := url.Parse("https://example.com/article")
	b.ResetTimer()
	for b.Loop() {
		_, _ = ext.Extract(benchHTML, pageURL)
	}
}
