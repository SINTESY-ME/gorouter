package app

import "strings"

const (
	minSubstringLen = 4
	maxLevenRatio   = 6
	minLevenLen     = 3
)

var safeSuffixes = []string{
	"-free",
	"-latest",
	"-preview",
	"-alpha",
	"-beta",
}

type candidateMatch struct {
	key   string
	score int
}

func findBestFuzzyMatch(normModel string, entries map[string]registryEntry) (registryEntry, bool) {
	if len(entries) == 0 {
		return registryEntry{}, false
	}

	candidates := collectCandidates(normModel, entries)
	if len(candidates) == 0 {
		return registryEntry{}, false
	}

	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.score < best.score {
			best = c
		}
	}

	return entries[best.key], true
}

func collectCandidates(normModel string, entries map[string]registryEntry) []candidateMatch {
	var candidates []candidateMatch

	if stripped, ok := tryStripSafeSuffixes(normModel); ok {
		if e, exists := entries[stripped]; exists && HasPricing(e.Pricing) {
			score := len(normModel) - len(stripped)
			candidates = append(candidates, candidateMatch{key: stripped, score: score})
		}
	}

	if key, ok := findLongestContainment(normModel, entries); ok {
		score := len(normModel) - len(key)
		candidates = append(candidates, candidateMatch{key: key, score: score})
	}

	if key, ok := findLevenshteinMatch(normModel, entries); ok {
		score := levenshteinDistance(normModel, key)
		candidates = append(candidates, candidateMatch{key: key, score: score})
	}

	return candidates
}

func tryStripSafeSuffixes(normModel string) (string, bool) {
	for _, suffix := range safeSuffixes {
		if strings.HasSuffix(normModel, suffix) {
			return strings.TrimSuffix(normModel, suffix), true
		}
	}
	return "", false
}

func findLongestContainment(normModel string, entries map[string]registryEntry) (string, bool) {
	var bestKey string
	for key := range entries {
		if len(key) < minSubstringLen {
			continue
		}
		if strings.Contains(normModel, key) && len(key) > len(bestKey) {
			bestKey = key
		}
	}
	return bestKey, bestKey != ""
}

func findLevenshteinMatch(normModel string, entries map[string]registryEntry) (string, bool) {
	maxDist := levenshteinThreshold(len(normModel))
	var bestKey string
	bestDist := maxDist + 1

	for key, e := range entries {
		if len(key) < minLevenLen {
			continue
		}
		if !HasPricing(e.Pricing) {
			continue
		}
		dist := levenshteinDistance(normModel, key)
		if dist <= maxDist && dist < bestDist {
			bestKey = key
			bestDist = dist
		}
	}

	return bestKey, bestKey != ""
}

func levenshteinThreshold(modelLen int) int {
	threshold := modelLen / maxLevenRatio
	if threshold < minLevenLen {
		threshold = minLevenLen
	}
	return threshold
}

func levenshteinDistance(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	if la > lb {
		a, b = b, a
		la, lb = lb, la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)

	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = minInt(
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
			)
		}
		prev, curr = curr, prev
	}

	return prev[lb]
}

func minInt(vals ...int) int {
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}
