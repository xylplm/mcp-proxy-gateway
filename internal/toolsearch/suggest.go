package toolsearch

import (
	"sort"
	"unicode/utf8"
)

const (
	maxSuggestions         = 3
	maxSuggestionTermRunes = 64
)

func (ix *Index) suggestions(terms []string) []string {
	candidates := make(map[string]struct{})
	for _, term := range terms {
		termRunes := utf8.RuneCountInString(term)
		if termRunes < 2 || termRunes > maxSuggestionTermRunes {
			continue
		}
		for token := range ix.allTokens {
			if hasPrefix(token, term) {
				candidates[token] = struct{}{}
			}
		}
	}
	if len(candidates) == 0 {
		for _, term := range terms {
			termRunes := utf8.RuneCountInString(term)
			if termRunes < 4 || termRunes > maxSuggestionTermRunes {
				continue
			}
			for token := range ix.allTokens {
				if editDistanceAtMost(term, token, 2) {
					candidates[token] = struct{}{}
				}
			}
		}
	}
	if len(candidates) == 0 {
		return ix.topUpstreams()
	}
	out := make([]string, 0, len(candidates))
	for token := range candidates {
		out = append(out, token)
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := ix.allTokens[out[i]], ix.allTokens[out[j]]
		if left != right {
			return left > right
		}
		if utf8.RuneCountInString(out[i]) != utf8.RuneCountInString(out[j]) {
			return utf8.RuneCountInString(out[i]) < utf8.RuneCountInString(out[j])
		}
		return out[i] < out[j]
	})
	if len(out) > maxSuggestions {
		out = out[:maxSuggestions]
	}
	return out
}

// topUpstreams is the final zero-result fallback. It gives the caller a
// concrete next browsing target even when neither prefix nor typo correction
// can produce a useful token.
func (ix *Index) topUpstreams() []string {
	counts := make(map[string]int)
	for _, doc := range ix.docs {
		seen := make(map[string]struct{}, len(doc.UpstreamNames))
		for _, name := range doc.UpstreamNames {
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			counts[name]++
		}
	}
	if len(counts) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(counts))
	for name := range counts {
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool {
		if counts[out[i]] != counts[out[j]] {
			return counts[out[i]] > counts[out[j]]
		}
		return out[i] < out[j]
	})
	if len(out) > maxSuggestions {
		out = out[:maxSuggestions]
	}
	return out
}

// editDistanceAtMost calculates only enough of Levenshtein distance to answer
// the suggestion decision. The early exit prevents a zero-result query from
// doing full quadratic work against every indexed token.
func editDistanceAtMost(left, right string, limit int) bool {
	a, b := []rune(left), []rune(right)
	if abs(len(a)-len(b)) > limit {
		return false
	}
	if len(a) < len(b) {
		a, b = b, a
	}
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, r := range a {
		current := make([]int, len(b)+1)
		current[0] = i + 1
		rowMin := current[0]
		for j, other := range b {
			cost := 0
			if r != other {
				cost = 1
			}
			current[j+1] = min(min(current[j]+1, previous[j+1]+1), previous[j]+cost)
			rowMin = min(rowMin, current[j+1])
		}
		if rowMin > limit {
			return false
		}
		previous = current
	}
	return previous[len(b)] <= limit
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
