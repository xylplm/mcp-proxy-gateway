package domain

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// UpstreamNameMaxRunes 与 manager 层名称上限一致。
	UpstreamNameMaxRunes = 100
	// UpstreamNameMinRunes 与 manager 层名称下限一致。
	UpstreamNameMinRunes = 1
)

// SuggestCopyName 为复制上游生成不与 existing 冲突的唯一名称。
//
// 命名序列：`{name} 副本` → `{name} 副本2` → …；超长时截断源名再拼接。
// existing 按大小写不敏感比较。source 为空时使用「上游」。
// 该函数不访问存储，仅供管理台/未来复制 API 复用。
func SuggestCopyName(source string, existing []string) string {
	base := strings.TrimSpace(source)
	if base == "" {
		base = "上游"
	}

	occupied := make(map[string]struct{}, len(existing))
	for _, name := range existing {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		occupied[key] = struct{}{}
	}

	try := func(candidate string) (string, bool) {
		name := truncateRunes(strings.TrimSpace(candidate), UpstreamNameMaxRunes)
		if name == "" {
			return "", false
		}
		if _, ok := occupied[strings.ToLower(name)]; ok {
			return "", false
		}
		return name, true
	}

	withSuffix := func(baseName, suffix string) string {
		maxBase := max(UpstreamNameMaxRunes-utf8.RuneCountInString(suffix), 1)
		return truncateRunes(baseName, maxBase) + suffix
	}

	if name, ok := try(withSuffix(base, " 副本")); ok {
		return name
	}
	for i := 2; i <= 10000; i++ {
		if name, ok := try(withSuffix(base, fmt.Sprintf(" 副本%d", i))); ok {
			return name
		}
	}

	// 极端兜底：附加数字后缀，保证可提交且不与 existing 冲突。
	for i := 1; i <= 100000; i++ {
		if name, ok := try(withSuffix(base, fmt.Sprintf("-%d", i))); ok {
			return name
		}
		if name, ok := try(fmt.Sprintf("上游-%d", i)); ok {
			return name
		}
	}
	// 理论上不可达：existing 覆盖了全部候选时仍返回截断后的确定性名称。
	return truncateRunes(fmt.Sprintf("上游-%d", len(occupied)+1), UpstreamNameMaxRunes)
}

func truncateRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	var b strings.Builder
	b.Grow(len(value))
	n := 0
	for _, r := range value {
		if n >= max {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
