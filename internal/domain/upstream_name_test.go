package domain

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSuggestCopyNameBasicSequence(t *testing.T) {
	t.Parallel()

	if got := SuggestCopyName("GitHub", nil); got != "GitHub 副本" {
		t.Fatalf("empty existing: got %q", got)
	}
	if got := SuggestCopyName("GitHub", []string{"GitHub 副本"}); got != "GitHub 副本2" {
		t.Fatalf("one copy exists: got %q", got)
	}
	if got := SuggestCopyName("GitHub", []string{"GitHub 副本", "GitHub 副本2", "github 副本3"}); got != "GitHub 副本4" {
		t.Fatalf("case-insensitive conflict: got %q", got)
	}
}

func TestSuggestCopyNameEmptyAndLong(t *testing.T) {
	t.Parallel()

	if got := SuggestCopyName("   ", []string{"上游 副本"}); got != "上游 副本2" {
		t.Fatalf("blank source: got %q", got)
	}

	long := strings.Repeat("测", 98)
	got := SuggestCopyName(long, nil)
	if utf8.RuneCountInString(got) > UpstreamNameMaxRunes {
		t.Fatalf("name too long: %d runes", utf8.RuneCountInString(got))
	}
	if !strings.HasSuffix(got, "副本") {
		t.Fatalf("expected 副本 suffix, got %q", got)
	}
}

func TestSuggestCopyNameAlwaysUnique(t *testing.T) {
	t.Parallel()

	existing := []string{"服务 副本"}
	for range 20 {
		name := SuggestCopyName("服务", existing)
		for _, item := range existing {
			if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(name)) {
				t.Fatalf("collision with existing %q -> %q", item, name)
			}
		}
		if n := utf8.RuneCountInString(name); n < UpstreamNameMinRunes || n > UpstreamNameMaxRunes {
			t.Fatalf("invalid length %d for %q", n, name)
		}
		existing = append(existing, name)
	}
}
