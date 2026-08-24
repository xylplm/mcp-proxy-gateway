package toolsearch

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestTokenizeCommonToolFormats(t *testing.T) {
	tests := map[string][]string{
		"reach_twitter_user_timeline": {"reach", "twitter", "user", "timeline"},
		"userTimeline":                {"user", "timeline"},
		"HTTPServer":                  {"http", "server"},
		"vm100":                       {"vm", "100"},
		"虚拟机":                         {"虚", "拟", "机", "虚拟", "拟机"},
		"___":                         {},
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			if got := Tokenize(input); !reflect.DeepEqual(got, want) {
				t.Fatalf("Tokenize(%q)=%v, want %v", input, got, want)
			}
		})
	}
}

func TestTokenizeLimitPreservesOrderWithoutMaterializingCJKTail(t *testing.T) {
	input := strings.Repeat("虚拟机", 512)
	all := Tokenize(input)
	limited := tokenize(input, descTokenLimit)
	if len(limited) != descTokenLimit || !reflect.DeepEqual(limited, all[:descTokenLimit]) {
		t.Fatalf("limited tokenizer must preserve the full tokenizer prefix: got=%d want=%d", len(limited), descTokenLimit)
	}
}

func TestSearchRanksAndPagesDeterministically(t *testing.T) {
	ix := Build([]Doc{
		{Name: "reach_twitter_user_timeline", Description: "read a Twitter user timeline"},
		{Name: "create_pull_request", Description: "create a GitHub pull request", UpstreamNames: []string{"GitHub"}},
		{Name: "vm_list", Description: "list virtual machines", UpstreamNames: []string{"PVE"}},
		{Name: "alpha_list", Description: "list objects"},
		{Name: "beta_list", Description: "list objects"},
	})
	tests := []struct{ query, first string }{
		{"github create pr", "create_pull_request"},
		{"twitter timeline", "reach_twitter_user_timeline"},
		{"list vms", "vm_list"},
		{"create_pull_request", "create_pull_request"},
		{"帮我在 github 上创建一个 pr", "create_pull_request"},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			result := ix.Search(tt.query, 10, 0)
			if len(result.Hits) == 0 || ix.docs[result.Hits[0].DocIndex].Name != tt.first {
				t.Fatalf("Search(%q) first=%v, want %q", tt.query, result.Hits, tt.first)
			}
		})
	}

	all := ix.Search("list", 10, 0)
	first := ix.Search("list", 1, 0)
	second := ix.Search("list", 1, 1)
	if all.Total != len(all.Hits) || all.Total < 2 || first.Total != second.Total || first.Hits[0].DocIndex == second.Hits[0].DocIndex {
		t.Fatalf("pagination is not stable: all=%+v first=%+v second=%+v", all, first, second)
	}
	if got := ix.Search("list", 10, 999); len(got.Hits) != 0 || got.Total != all.Total {
		t.Fatalf("out of range page wrong: %+v", got)
	}
}

func TestSearchFallbackAndSuggestions(t *testing.T) {
	ix := Build([]Doc{{Name: "create_pull_request", Description: "create a pull request"}, {Name: "twitter_timeline", Description: "timeline"}})
	fallback := ix.Search("ull", 10, 0)
	if !fallback.Fallback || len(fallback.Hits) != 1 {
		t.Fatalf("expected substring fallback, got %+v", fallback)
	}
	result := ix.Search("timelinx", 10, 0)
	if result.Total != 0 || len(result.Suggestions) == 0 {
		t.Fatalf("expected deterministic suggestion, got %+v", result)
	}
	again := ix.Search("timelinx", 10, 0)
	if !reflect.DeepEqual(result.Suggestions, again.Suggestions) {
		t.Fatalf("suggestions not deterministic: %v != %v", result.Suggestions, again.Suggestions)
	}
	if got := ix.Search("   ", 10, 0); got.Total != 0 || len(got.Hits) != 0 {
		t.Fatalf("empty query should not browse tools: %+v", got)
	}
}

func TestSearchBoundsUntrustedQueryAndUpstreamDescriptions(t *testing.T) {
	description := "target " + strings.Repeat("filler ", maxDescriptionRunes) + "tail-only"
	ix := Build([]Doc{{Name: "bounded", Description: description}})
	if len(ix.indexed[0].descTokens) > descTokenLimit {
		t.Fatalf("description token set exceeds limit: %d", len(ix.indexed[0].descTokens))
	}
	if _, ok := ix.indexed[0].descTokens["tail"]; ok {
		t.Fatalf("description content after the raw safety bound must not be indexed")
	}
	if !QueryTooLong(strings.Repeat("a", MaxQueryRunes+1)) {
		t.Fatal("query exceeding the fixed limit should be rejected")
	}
	if result := ix.Search(strings.Repeat("a", MaxQueryRunes+1), 10, 0); result.Total != 0 || len(result.Hits) != 0 {
		t.Fatalf("overlong query should not enter search work: %+v", result)
	}
}

func TestSearchBoundsUntrustedIdentityFields(t *testing.T) {
	tooLong := strings.Repeat("x", maxIndexedFieldRunes) + "tail-only"
	ix := Build([]Doc{{
		Name:          tooLong,
		OriginalName:  tooLong,
		UpstreamNames: []string{tooLong},
		UpstreamTags:  []string{tooLong},
	}})
	for token := range ix.allTokens {
		if len([]rune(token)) > maxIndexedFieldRunes {
			t.Fatalf("untrusted identity token exceeds index bound: %d", len([]rune(token)))
		}
	}
	if result := ix.Search("tail-only", 10, 0); result.Total != 0 {
		t.Fatalf("content after an identity field's safety bound must not be indexed: %+v", result)
	}
}

func TestSearchCapsDistinctQueryTerms(t *testing.T) {
	terms := make([]string, 0, maxQueryTerms+1)
	for i := 0; i < maxQueryTerms; i++ {
		terms = append(terms, fmt.Sprintf("miss%02d", i))
	}
	terms = append(terms, "target")
	ix := Build([]Doc{{Name: "target"}})
	if result := ix.Search(strings.Join(terms, " "), 10, 0); result.Total != 0 {
		t.Fatalf("terms beyond the fixed query budget must not enter matching work: %+v", result)
	}
}

func TestSearchFallsBackToTopUpstreamsWhenNoTokenSuggestionExists(t *testing.T) {
	ix := Build([]Doc{
		{Name: "one", UpstreamNames: []string{"PVE"}},
		{Name: "two", UpstreamNames: []string{"GitHub"}},
		{Name: "three", UpstreamNames: []string{"PVE"}},
	})
	result := ix.Search("zzzzzz", 10, 0)
	if result.Total != 0 || !reflect.DeepEqual(result.Suggestions, []string{"PVE", "GitHub"}) {
		t.Fatalf("expected ordered upstream fallback, got %+v", result)
	}
}
