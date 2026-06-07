package manager

import (
	"errors"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

// 本文件实现排序请求完整性校验的属性测试（任务 9.4，Property 3），
// 针对 reorder.go 中的纯函数 ValidateReorder(registered, orderedIDs)。
//
// 为避免与同包并行任务的测试标识冲突（backoff 用 p14、lifecycle 用 p15），
// 本文件内的辅助生成器与判定函数统一采用 p3 前缀。
//
// 校验目标（Req 3.5）：当且仅当 orderedIDs 是「已注册标识集合」的恰好一次排列
// （无重复、无未注册、无缺失）时返回 nil；否则返回 Code=VALIDATION 且携带
// orderedIDs 字段说明的 *domain.APIError。期望结果由不依赖被测实现的独立参考
// 判定 p3IsExactPermutation 推导。

// p3RegisteredPool 是「已注册标识」的取值池。已注册集合从中按 distinct 采样。
var p3RegisteredPool = []string{"a", "b", "c", "d", "e", "f", "g", "h"}

// p3ForeignPool 是「未注册标识」的取值池，与 p3RegisteredPool 不相交，
// 从而保证从中取出的标识必然不属于任何已注册集合。
var p3ForeignPool = []string{"x", "y", "z", "未注册", "ghost-1", "ghost-2"}

// p3GenRegistered 生成一个由 distinct 标识构成的已注册集合（含空集合），
// 元素取自 p3RegisteredPool，最长等于池大小。
func p3GenRegistered() *rapid.Generator[[]string] {
	return rapid.SliceOfNDistinct(
		rapid.SampledFrom(p3RegisteredPool),
		0, len(p3RegisteredPool),
		func(s string) string { return s },
	)
}

// p3insert 在 base 的 pos 位置插入 v，返回新切片（不修改 base）。
func p3insert(base []string, pos int, v string) []string {
	out := make([]string, 0, len(base)+1)
	out = append(out, base[:pos]...)
	out = append(out, v)
	out = append(out, base[pos:]...)
	return out
}

// p3IsExactPermutation 是排序完整性的独立参考判定：当且仅当 ordered 不含重复、
// 且其标识集合与 registered 的标识集合完全相等（无未注册、无缺失）时为真。
// 该判定不调用被测实现，作为期望结果的独立对照。
func p3IsExactPermutation(registered, ordered []string) bool {
	seen := make(map[string]struct{}, len(ordered))
	for _, id := range ordered {
		if _, dup := seen[id]; dup {
			return false // 含重复
		}
		seen[id] = struct{}{}
	}
	regSet := make(map[string]struct{}, len(registered))
	for _, id := range registered {
		regSet[id] = struct{}{}
	}
	// ordered 无重复后，集合相等 ⇔ 大小相等且 ordered ⊆ registered（双射）。
	if len(seen) != len(regSet) {
		return false
	}
	for id := range seen {
		if _, ok := regSet[id]; !ok {
			return false // 含未注册标识
		}
	}
	return true
}

// p3DrawOrdered 基于已注册集合生成一个待校验的排序请求，覆盖以下情形：
//   - kind 0：合法排列（对 registered 洗牌）；
//   - kind 1：含重复（在排列上复制一个已注册标识）；
//   - kind 2：含未注册标识（插入一个外部标识）；
//   - kind 3：缺失已注册标识（从排列中删除一个标识）；
//   - kind 4：任意切片（从「已注册 ∪ 外部」池中任取，可重复、任意长度），
//     由参考判定裁决，进一步拓宽合法/非法组合的覆盖面。
func p3DrawOrdered(t *rapid.T, registered []string) []string {
	kind := rapid.IntRange(0, 4).Draw(t, "orderKind")
	switch kind {
	case 0:
		return rapid.Permutation(append([]string(nil), registered...)).Draw(t, "permutation")
	case 1:
		base := rapid.Permutation(append([]string(nil), registered...)).Draw(t, "dupBase")
		if len(registered) == 0 {
			// 无可复制的已注册标识时，退化为同一外部标识出现两次（仍为「含重复」）。
			id := rapid.SampledFrom(p3ForeignPool).Draw(t, "dupLone")
			return []string{id, id}
		}
		dup := rapid.SampledFrom(registered).Draw(t, "dupID")
		pos := rapid.IntRange(0, len(base)).Draw(t, "dupPos")
		return p3insert(base, pos, dup)
	case 2:
		base := rapid.Permutation(append([]string(nil), registered...)).Draw(t, "unregBase")
		foreign := rapid.SampledFrom(p3ForeignPool).Draw(t, "foreignID")
		pos := rapid.IntRange(0, len(base)).Draw(t, "unregPos")
		return p3insert(base, pos, foreign)
	case 3:
		if len(registered) == 0 {
			// 空注册集合无从「缺失」，改注入一个外部标识使其非法（空注册仅空排列合法）。
			return []string{rapid.SampledFrom(p3ForeignPool).Draw(t, "emptyMissForeign")}
		}
		base := rapid.Permutation(append([]string(nil), registered...)).Draw(t, "missBase")
		idx := rapid.IntRange(0, len(base)-1).Draw(t, "dropIdx")
		out := make([]string, 0, len(base)-1)
		out = append(out, base[:idx]...)
		out = append(out, base[idx+1:]...)
		return out
	default:
		pool := append(append([]string(nil), registered...), p3ForeignPool...)
		return rapid.SliceOfN(rapid.SampledFrom(pool), 0, 10).Draw(t, "arbitrary")
	}
}

// p3assertReorder 断言 ValidateReorder 的返回值与期望一致：
//   - 期望合法时必须返回 nil；
//   - 期望非法时必须返回 Code=VALIDATION 且 Fields 含 orderedIDs 的 *domain.APIError。
func p3assertReorder(t *rapid.T, err error, wantValid bool, registered, ordered []string) {
	t.Helper()
	if wantValid {
		if err != nil {
			t.Fatalf("合法排列应被接受却被拒绝：registered=%v ordered=%v err=%v", registered, ordered, err)
		}
		return
	}
	if err == nil {
		t.Fatalf("非法排序应被拒绝却返回 nil：registered=%v ordered=%v", registered, ordered)
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("非法排序应返回 *domain.APIError：registered=%v ordered=%v err=%v (%T)", registered, ordered, err, err)
	}
	if apiErr.Code != domain.CodeValidation {
		t.Fatalf("非法排序应返回 VALIDATION 错误：code=%s registered=%v ordered=%v", apiErr.Code, registered, ordered)
	}
	if _, ok := apiErr.Fields["orderedIDs"]; !ok {
		t.Fatalf("校验错误应携带 orderedIDs 字段说明：fields=%v registered=%v ordered=%v", apiErr.Fields, registered, ordered)
	}
}

// Feature: mcp-proxy-gateway, Property 3: 排序请求完整性校验
//
// Validates: Requirements 3.5
//
// 对任意已注册上游标识集合与提交的排序：当且仅当提交排序为该集合的恰好一次排列
// （无重复、无未注册、无缺失）时 ValidateReorder 返回 nil；否则返回携带 orderedIDs
// 字段说明的 VALIDATION 错误。期望由独立参考判定 p3IsExactPermutation 推导，
// 生成器覆盖合法排列、含重复、含未注册、缺失标识与任意组合五类情形。
func TestProperty3ReorderIntegrityValidation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		registered := p3GenRegistered().Draw(t, "registered")
		ordered := p3DrawOrdered(t, registered)

		err := ValidateReorder(registered, ordered)
		wantValid := p3IsExactPermutation(registered, ordered)

		p3assertReorder(t, err, wantValid, registered, ordered)
	})
}
