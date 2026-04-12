package search

import (
	"github.com/anatolykoptev/go-engine/sources"
	"github.com/anatolykoptev/go-engine/websearch"
)

// FilterByScore removes results below minScore, keeping at least minKeep.
func FilterByScore(results []sources.Result, minScore float64, minKeep int) []sources.Result {
	return websearch.FilterByScore(results, minScore, minKeep)
}

// DedupByDomain limits results to maxPerDomain per domain.
func DedupByDomain(results []sources.Result, maxPerDomain int) []sources.Result {
	return websearch.DedupByDomain(results, maxPerDomain)
}
