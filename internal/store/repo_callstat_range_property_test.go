package store

import (
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

// p22BaseMillis 为生成调用时间的基准时刻（2024-01-01T00:00:00Z 的毫秒时间戳）。
// 以固定基准 + 随机毫秒偏移构造 called_at，使端点重合、区间内外等情形都能稳定覆盖。
const p22BaseMillis int64 = 1_704_067_200_000

// inClosedRange 复刻仓储层 SQL 谓词 `called_at >= start AND called_at <= end` 的闭区间语义
// （Req 16.5），作为统计时间过滤的纯函数内存模型，使该判定脱离真实数据库可被属性测试验证。
//
// 仓储层 CountByUpstream / CountByAPIKey / TopTools 均以同一谓词筛选记录，故本模型即三者
// 统计计入条件的统一抽象：仅当 called_at 落在闭区间 [start, end]（含两端点）内才计入。
func inClosedRange(calledAt, start, end time.Time) bool {
	return !calledAt.Before(start) && !calledAt.After(end)
}

// genP22Records 生成一组调用统计记录，called_at 由固定基准叠加随机毫秒偏移得到，
// 偏移范围足够宽，确保区间内、区间外、恰好落在端点的记录都可能出现。
func genP22Records() *rapid.Generator[[]CallStatRecord] {
	return rapid.Custom(func(t *rapid.T) []CallStatRecord {
		n := rapid.IntRange(0, 60).Draw(t, "记录数")
		recs := make([]CallStatRecord, 0, n)
		for i := 0; i < n; i++ {
			offset := rapid.Int64Range(-86_400_000, 86_400_000).Draw(t, "毫秒偏移")
			recs = append(recs, CallStatRecord{
				OriginalName: "tool",
				CalledAt:     time.UnixMilli(p22BaseMillis + offset).UTC(),
				Success:      true,
			})
		}
		return recs
	})
}

// Feature: mcp-proxy-gateway, Property 22: 统计时间范围闭区间过滤
//
// Validates: Requirements 16.5, 16.7
//
// 对任意调用记录集合与由开始/结束时间构成的范围，验证两条不变量：
//   - 当开始时间不晚于结束时间时（合法范围），统计仅计入 called_at 落在闭区间 [start, end]
//     内的记录，且两端点（called_at 恰等于 start 或 end）均被计入、区间外记录一律排除
//     （Req 16.5）。以纯函数模型 inClosedRange 复刻仓储 SQL 谓词进行逐条比对；
//   - 当开始时间严格晚于结束时间时，范围校验 validateRange 拒绝该查询并返回 CodeValidation
//     范围无效错误（Req 16.7）。三个统计方法均在查询前调用该校验，故校验其行为即覆盖全部入口。
//
// 闭区间过滤的真实实现位于仓储层 SQL（`called_at >= $1 AND called_at <= $2`），依赖数据库；
// 此处以等价的内存模型验证其语义，并直接调用生产校验函数 validateRange 验证范围拒绝逻辑。
func TestProperty22StatRangeClosedIntervalFilter(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		recs := genP22Records().Draw(t, "调用记录")

		// 由两个独立毫秒偏移构造范围端点，排序后得到 lo <= hi 作为合法范围。
		a := rapid.Int64Range(-86_400_000, 86_400_000).Draw(t, "端点A偏移")
		b := rapid.Int64Range(-86_400_000, 86_400_000).Draw(t, "端点B偏移")
		lo, hi := a, b
		if lo > hi {
			lo, hi = hi, lo
		}
		start := time.UnixMilli(p22BaseMillis + lo).UTC()
		end := time.UnixMilli(p22BaseMillis + hi).UTC()

		// ---- 不变量一：合法范围下闭区间过滤语义（Req 16.5）----
		if err := validateRange(start, end); err != nil {
			t.Fatalf("合法范围（start<=end）不应被拒绝：start=%v end=%v err=%v", start, end, err)
		}

		// 逐条比对：计入 <=> called_at 落在闭区间内（含端点）。
		var counted []time.Time
		for _, rec := range recs {
			in := inClosedRange(rec.CalledAt, start, end)
			// 区间外记录必须排除。
			if !in {
				if !rec.CalledAt.Before(start) && !rec.CalledAt.After(end) {
					t.Fatalf("内存模型自洽性破坏：%v 既判定区间外又落在 [%v,%v] 内",
						rec.CalledAt, start, end)
				}
				continue
			}
			// 计入的记录必须满足 start <= called_at <= end（含端点）。
			if rec.CalledAt.Before(start) {
				t.Fatalf("计入了早于开始时间的记录：called_at=%v < start=%v", rec.CalledAt, start)
			}
			if rec.CalledAt.After(end) {
				t.Fatalf("计入了晚于结束时间的记录：called_at=%v > end=%v", rec.CalledAt, end)
			}
			counted = append(counted, rec.CalledAt)
		}

		// 端点必被计入：分别以恰好等于 start、end 的记录验证两端点闭合。
		if !inClosedRange(start, start, end) {
			t.Fatalf("开始端点 %v 应被计入闭区间 [%v,%v]", start, start, end)
		}
		if !inClosedRange(end, start, end) {
			t.Fatalf("结束端点 %v 应被计入闭区间 [%v,%v]", end, start, end)
		}
		// 紧邻区间外侧的时刻必被排除（早于 start 1ms、晚于 end 1ms）。
		if inClosedRange(start.Add(-time.Millisecond), start, end) {
			t.Fatalf("早于开始端点 1ms 的时刻不应被计入")
		}
		if inClosedRange(end.Add(time.Millisecond), start, end) {
			t.Fatalf("晚于结束端点 1ms 的时刻不应被计入")
		}

		// 计入条数应等于「独立暴力筛选」的结果，确保模型不漏不重。
		var brute []time.Time
		for _, rec := range recs {
			ms := rec.CalledAt.UnixMilli()
			if ms >= start.UnixMilli() && ms <= end.UnixMilli() {
				brute = append(brute, rec.CalledAt)
			}
		}
		if len(counted) != len(brute) {
			t.Fatalf("计入条数与暴力筛选不一致：模型=%d 暴力=%d（范围 [%v,%v]）",
				len(counted), len(brute), start, end)
		}
		sort.Slice(counted, func(i, j int) bool { return counted[i].Before(counted[j]) })
		sort.Slice(brute, func(i, j int) bool { return brute[i].Before(brute[j]) })
		for i := range counted {
			if !counted[i].Equal(brute[i]) {
				t.Fatalf("计入集合与暴力筛选不一致：第 %d 项 %v != %v", i, counted[i], brute[i])
			}
		}

		// ---- 不变量二：开始严格晚于结束时拒绝并返回范围无效校验错误（Req 16.7）----
		// 仅当 lo<hi（存在严格更早的端点）时才能构造 start>end，否则 lo==hi 跳过该分支。
		if lo < hi {
			badStart, badEnd := end, start // start>end
			err := validateRange(badStart, badEnd)
			if err == nil {
				t.Fatalf("开始时间晚于结束时间应被拒绝：start=%v end=%v", badStart, badEnd)
			}
			var apiErr *domain.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeValidation {
				t.Fatalf("范围无效应返回 CodeValidation 校验错误，实际：%v", err)
			}
		}
	})
}
