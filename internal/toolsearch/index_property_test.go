package toolsearch

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// Feature: mcp-proxy-gateway, Property 31: 工具检索分词稳定且不产生空词元
//
// Validates: Requirements 11.4
func TestPropertyTokenizeStable(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		raw := rapid.StringMatching(`[A-Za-z0-9_./:\- ]{0,80}`).Draw(t, "raw")
		tokens := Tokenize(raw)
		for _, token := range tokens {
			if token == "" {
				t.Fatalf("Tokenize(%q) produced an empty token", raw)
			}
		}
		again := Tokenize(strings.Join(tokens, " "))
		if strings.Join(tokens, "\x00") != strings.Join(again, "\x00") {
			t.Fatalf("Tokenize is not stable: raw=%q first=%v second=%v", raw, tokens, again)
		}
	})
}

func TestTokenizeCJKTokenSetStable(t *testing.T) {
	first := Tokenize("列出虚拟机快照")
	second := Tokenize(strings.Join(first, " "))
	if !sameTokenSet(first, second) {
		t.Fatalf("CJK token set is not stable: first=%v second=%v", first, second)
	}
}

func sameTokenSet(left, right []string) bool {
	leftSet := make(map[string]struct{}, len(left))
	rightSet := make(map[string]struct{}, len(right))
	for _, token := range left {
		leftSet[token] = struct{}{}
	}
	for _, token := range right {
		rightSet[token] = struct{}{}
	}
	if len(leftSet) != len(rightSet) {
		return false
	}
	for token := range leftSet {
		if _, ok := rightSet[token]; !ok {
			return false
		}
	}
	return true
}
