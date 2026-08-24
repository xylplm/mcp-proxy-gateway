package toolsearch

import (
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	descTokenLimit = 200
	// MaxQueryRunes bounds work performed for an untrusted natural-language
	// query. It is intentionally generous for MCP clients while preventing one
	// request from turning phrase expansion and token matching into a CPU sink.
	MaxQueryRunes       = 512
	maxDescriptionRunes = 4096
	// Remote MCP metadata is untrusted. Indexing only the leading portion of
	// identity fields keeps one malformed tool name or upstream label from
	// growing the cached index or typo-suggestion work without affecting the
	// original metadata returned by discovery.
	maxIndexedFieldRunes  = 512
	maxQueryTerms         = 32
	minPrefixLen          = 3
	synonymDiscount       = 0.6
	reversePrefixDiscount = 0.7
)

// Doc is the searchable projection of one already-authorized tool.
type Doc struct {
	Name          string
	OriginalName  string
	Description   string
	UpstreamNames []string
	UpstreamTags  []string
}

// Hit describes a matching document. DocIndex points into the docs passed to
// Build, allowing callers to keep the original domain type outside this pure
// package.
type Hit struct {
	DocIndex int
	Score    float64
	Covered  int
	Matched  []string
}

// Result contains one stable page of hits plus total hit count before paging.
type Result struct {
	Hits        []Hit
	Total       int
	Fallback    bool
	Suggestions []string
}

type indexedDoc struct {
	nameTokens map[string]struct{}
	origTokens map[string]struct{}
	descTokens map[string]struct{}
	upTokens   map[string]struct{}
	nameRaw    string
	descRaw    string
}

// Index is immutable after Build and safe for concurrent searches.
type Index struct {
	docs      []Doc
	indexed   []indexedDoc
	allTokens map[string]int
}

// Build creates a reusable index for a tool collection. Input order is kept
// exactly, and each Hit.DocIndex refers to that original order.
func Build(docs []Doc) *Index {
	safeDocs := append([]Doc(nil), docs...)
	for index := range safeDocs {
		safeDocs[index].Description = truncateRunes(safeDocs[index].Description, maxDescriptionRunes)
		safeDocs[index].UpstreamNames = append([]string(nil), safeDocs[index].UpstreamNames...)
		safeDocs[index].UpstreamTags = append([]string(nil), safeDocs[index].UpstreamTags...)
	}
	ix := &Index{
		docs:      safeDocs,
		indexed:   make([]indexedDoc, 0, len(safeDocs)),
		allTokens: make(map[string]int),
	}
	for index, doc := range safeDocs {
		nameValue := truncateRunes(doc.Name, maxIndexedFieldRunes)
		originalNameValue := truncateRunes(doc.OriginalName, maxIndexedFieldRunes)
		descriptionValue := truncateRunes(doc.Description, maxDescriptionRunes)
		name := toTokenSet(Tokenize(nameValue), 0)
		orig := map[string]struct{}(nil)
		if normalizeQuery(originalNameValue) != normalizeQuery(nameValue) {
			orig = toTokenSet(Tokenize(originalNameValue), 0)
		}
		// Keep the existing "first 200 tokens" search semantics without
		// materializing tokens for an arbitrarily large upstream description.
		desc := toTokenSet(tokenize(descriptionValue, descTokenLimit), 0)
		up := make(map[string]struct{})
		for _, values := range [][]string{doc.UpstreamNames, doc.UpstreamTags} {
			for _, value := range values {
				for token := range toTokenSet(Tokenize(truncateRunes(value, maxIndexedFieldRunes)), 0) {
					up[token] = struct{}{}
				}
			}
		}
		ix.indexed = append(ix.indexed, indexedDoc{
			nameTokens: name,
			origTokens: orig,
			descTokens: desc,
			upTokens:   up,
			nameRaw:    normalizeQuery(nameValue),
			descRaw:    strings.ToLower(descriptionValue),
		})
		unique := make(map[string]struct{}, len(name)+len(orig)+len(up))
		for _, field := range []map[string]struct{}{name, orig, up} {
			for token := range field {
				unique[token] = struct{}{}
			}
		}
		for token := range unique {
			ix.allTokens[token]++
		}
		// Index only needs names and upstream names after preprocessing. Drop
		// large raw fields so a cached index does not retain both an upstream
		// description and its normalized fallback copy.
		safeDocs[index].OriginalName = ""
		safeDocs[index].Description = ""
		safeDocs[index].UpstreamTags = nil
	}
	return ix
}

// Search performs OR retrieval over the first fixed-size set of normalized
// query tokens and returns a deterministic page. Empty normalized queries
// intentionally return no hits; callers should use their listing action for
// browsing.
func (ix *Index) Search(query string, limit, offset int) Result {
	if ix == nil {
		return Result{Hits: []Hit{}, Suggestions: []string{}}
	}
	if limit <= 0 {
		limit = 1
	}
	if offset < 0 {
		offset = 0
	}
	if QueryTooLong(query) {
		return Result{Hits: []Hit{}, Suggestions: []string{}}
	}
	rawQuery := normalizeQuery(query)
	expandedQuery := applyPhraseSynonyms(rawQuery)
	rawTerms := dedupeKeepOrder(Tokenize(expandedQuery))
	terms := removeStopwords(rawTerms)
	if len(terms) == 0 {
		terms = rawTerms
	}
	if len(terms) > maxQueryTerms {
		terms = terms[:maxQueryTerms]
	}
	if len(terms) == 0 {
		return Result{Hits: []Hit{}, Suggestions: []string{}}
	}

	hits := make([]Hit, 0)
	for docIndex, doc := range ix.indexed {
		covered := 0
		score := 0.0
		matched := make([]string, 0, len(terms))
		for _, term := range terms {
			best := ix.fieldBest(doc, term)
			for _, synonym := range tokenSynonyms[term] {
				best = max(best, synonymDiscount*ix.fieldBest(doc, synonym))
			}
			if best == 0 {
				continue
			}
			covered++
			score += best
			matched = append(matched, term)
		}
		if covered == 0 {
			continue
		}
		score += wholeStringBonus(doc.nameRaw, rawQuery)
		hits = append(hits, Hit{DocIndex: docIndex, Score: score, Covered: covered, Matched: matched})
	}
	if len(hits) == 0 && rawQuery != "" {
		return ix.substringFallback(rawQuery, limit, offset)
	}
	sortHits(ix.docs, hits)
	return Result{Hits: sliceHits(hits, offset, limit), Total: len(hits), Suggestions: []string{}}
}

func (ix *Index) fieldBest(doc indexedDoc, term string) float64 {
	return max(
		matchScore(doc.nameTokens, term, 3.0),
		matchScore(doc.origTokens, term, 2.5),
		matchScore(doc.upTokens, term, 2.0),
		matchScore(doc.descTokens, term, 1.0),
	)
}

func matchScore(tokens map[string]struct{}, term string, base float64) float64 {
	if _, ok := tokens[term]; ok {
		return base
	}
	if len(tokens) == 0 || isCJK(term) || len([]rune(term)) < minPrefixLen {
		return 0
	}
	best := 0.0
	for token := range tokens {
		if isCJK(token) || len([]rune(token)) < minPrefixLen {
			continue
		}
		if strings.HasPrefix(token, term) {
			best = max(best, base*(2.0/3.0))
		}
		if strings.HasPrefix(term, token) {
			best = max(best, base*reversePrefixDiscount)
		}
	}
	return best
}

func (ix *Index) substringFallback(query string, limit, offset int) Result {
	hits := make([]Hit, 0)
	for index := range ix.docs {
		indexed := ix.indexed[index]
		if strings.Contains(indexed.nameRaw, query) || strings.Contains(indexed.descRaw, query) {
			hits = append(hits, Hit{DocIndex: index})
		}
	}
	if len(hits) == 0 {
		return Result{Hits: []Hit{}, Suggestions: ix.suggestions(dedupeKeepOrder(Tokenize(query)))}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		left, right := ix.docs[hits[i].DocIndex].Name, ix.docs[hits[j].DocIndex].Name
		if utf8.RuneCountInString(left) != utf8.RuneCountInString(right) {
			return utf8.RuneCountInString(left) < utf8.RuneCountInString(right)
		}
		return left < right
	})
	return Result{Hits: sliceHits(hits, offset, limit), Total: len(hits), Fallback: true, Suggestions: []string{}}
}

func sortHits(docs []Doc, hits []Hit) {
	sort.SliceStable(hits, func(i, j int) bool {
		left, right := hits[i], hits[j]
		if left.Covered != right.Covered {
			return left.Covered > right.Covered
		}
		if left.Score != right.Score {
			return left.Score > right.Score
		}
		leftName, rightName := docs[left.DocIndex].Name, docs[right.DocIndex].Name
		if utf8.RuneCountInString(leftName) != utf8.RuneCountInString(rightName) {
			return utf8.RuneCountInString(leftName) < utf8.RuneCountInString(rightName)
		}
		return leftName < rightName
	})
}

func wholeStringBonus(name, query string) float64 {
	if name == "" || query == "" {
		return 0
	}
	if name == query {
		return 12
	}
	if strings.HasPrefix(name, query) {
		return 6
	}
	if strings.Contains(name, query) {
		return 3
	}
	return 0
}

func toTokenSet(tokens []string, limit int) map[string]struct{} {
	out := make(map[string]struct{}, len(tokens))
	for index, token := range tokens {
		if limit > 0 && index >= limit {
			break
		}
		out[token] = struct{}{}
	}
	return out
}

func sliceHits(hits []Hit, offset, limit int) []Hit {
	if offset >= len(hits) {
		return []Hit{}
	}
	end := min(offset+limit, len(hits))
	out := make([]Hit, end-offset)
	copy(out, hits[offset:end])
	return out
}

func hasPrefix(value, prefix string) bool { return strings.HasPrefix(value, prefix) }

// QueryTooLong reports whether value exceeds the fixed query budget without
// allocating a []rune or scanning past the first disallowed rune.
func QueryTooLong(value string) bool {
	count := 0
	for range value {
		count++
		if count > MaxQueryRunes {
			return true
		}
	}
	return false
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	count := 0
	for byteIndex := range value {
		if count == limit {
			return value[:byteIndex]
		}
		count++
	}
	return value
}
