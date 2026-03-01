// Package extract provides article text extraction from HTML pages.
//
// Uses go-trafilatura as the primary extractor with a fallback chain:
// trafilatura -> goquery -> regex stripping. Returns clean text,
// markdown, and rich metadata (title, author, date, language).
package extract
