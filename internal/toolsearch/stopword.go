package toolsearch

// stopwords intentionally only contains grammatical words. Tool verbs such as
// list, get, set, and run must remain searchable.
var stopwords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "of": {}, "to": {}, "in": {}, "on": {},
	"at": {}, "for": {}, "with": {}, "and": {}, "or": {}, "is": {}, "are": {},
	"be": {}, "i": {}, "me": {}, "my": {}, "please": {},
	"的": {}, "了": {}, "我": {}, "你": {}, "帮": {}, "请": {}, "一": {}, "个": {},
	"把": {}, "给": {}, "帮我": {}, "一个": {}, "请问": {}, "怎么": {}, "如何": {}, "可以": {},
}

func removeStopwords(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if _, ok := stopwords[token]; !ok {
			out = append(out, token)
		}
	}
	return out
}
