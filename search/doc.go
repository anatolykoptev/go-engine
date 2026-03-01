// Package search provides web search aggregation across multiple engines.
//
// Defines the [SearchEngine] interface implemented by SearXNG, DuckDuckGo,
// and Startpage backends. The [Aggregator] performs concurrent fan-out
// queries with URL deduplication and score-based ranking.
package search
