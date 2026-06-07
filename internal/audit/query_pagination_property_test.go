package audit

import (
	"context"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/store"
	"pgregory.net/rapid"
)

// genAuditRows 生成一组审计记录用于分页属性测试。
//
// 每条记录赋予唯一自增 ID（1..n），发生时间从基准时刻起按随机非负增量累加，
// 因此可能出现「不同 ID 但相同发生时间」的情况，用于覆盖倒序时的同刻排序稳定性。
func genAuditRows() *rapid.Generator[[]store.AuditRecord] {
	return rapid.Custom(func(t *rapid.T) []store.AuditRecord {
		n := rapid.IntRange(0, 60).Draw(t, "记录数")
		rows := make([]store.AuditRecord, 0, n)
		cur := fixedNow
		for i := 0; i < n; i++ {
			// 增量为 0 时与上一条同刻，>0 时严格更晚。
			deltaSec := rapid.IntRange(0, 120).Draw(t, "时间增量秒")
			cur = cur.Add(time.Duration(deltaSec) * time.Second)
			rows = append(rows, store.AuditRecord{
				ID:         int64(i + 1),
				EventType:  store.AuditEventLogin,
				OccurredAt: cur,
			})
		}
		return rows
	})
}

// Feature: mcp-proxy-gateway, Property 24: 审计日志倒序分页
//
// Validates: Requirements 22.4
//
// 对任意审计记录集合与页大小（默认 20，范围 1-200），逐页调用 Service.List
// 遍历全部记录，验证：
//   - 生效页大小始终收敛到 [1, 200]，page≤0 归正为第 1 页；
//   - 单页内记录按发生时间倒序排列（同刻按 ID 倒序，与仓储语义一致）；
//   - 跨页拼接后整体仍保持倒序，相邻页边界不发生顺序逆转；
//   - 各页之间不重叠、不遗漏：合并所有页恰好覆盖全部记录各一次，
//     且总数与 Count 报告的 Total 一致。
func TestProperty24AuditLogReverseChronologicalPagination(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rows := genAuditRows().Draw(t, "审计记录")
		// 请求页大小覆盖：非正（触发默认回退）、范围内、超上界（触发收敛）。
		requestedSize := rapid.OneOf(
			rapid.IntRange(-5, 0),
			rapid.IntRange(1, maxPageSize),
			rapid.IntRange(maxPageSize+1, maxPageSize+100),
		).Draw(t, "请求页大小")
		// 配置默认页大小：含越界值，验证回退到硬编码默认 20。
		cfgDefault := rapid.IntRange(-1, maxPageSize+10).Draw(t, "配置默认页大小")

		repo := &testAuditRepo{rows: append([]store.AuditRecord{}, rows...)}
		svc, err := New(repo, fixedConfig{
			retentionDays:   defaultRetentionDays,
			pageSizeDefault: cfgDefault,
		})
		if err != nil {
			t.Fatalf("New 不应返回错误：%v", err)
		}
		svc.now = func() time.Time { return fixedNow }

		ctx := context.Background()

		// 先取第 1 页确定生效页大小，并据此遍历所有页。
		first, err := svc.List(ctx, 1, requestedSize)
		if err != nil {
			t.Fatalf("List 不应返回错误：%v", err)
		}
		size := first.PageSize
		if size < minPageSize || size > maxPageSize {
			t.Fatalf("生效页大小 %d 应收敛到 [%d, %d]", size, minPageSize, maxPageSize)
		}
		if first.Total != int64(len(rows)) {
			t.Fatalf("Total 应等于记录总数 %d，实际 %d", len(rows), first.Total)
		}

		// 逐页收集，直到出现空页（越过末尾）。
		var merged []store.AuditRecord
		for page := 1; ; page++ {
			res, err := svc.List(ctx, page, requestedSize)
			if err != nil {
				t.Fatalf("List 第 %d 页不应返回错误：%v", page, err)
			}
			if res.PageSize != size {
				t.Fatalf("各页生效页大小应一致：第 %d 页 %d，期望 %d", page, res.PageSize, size)
			}
			if len(res.Records) == 0 {
				break
			}
			if len(res.Records) > size {
				t.Fatalf("第 %d 页条数 %d 超过页大小 %d", page, len(res.Records), size)
			}
			merged = append(merged, res.Records...)
			// 非末页必须填满；末页可不足。通过下一轮空页判定末页，这里仅防御无限循环。
			if page > len(rows)+1 {
				t.Fatalf("分页遍历未在合理页数内终止，疑似不推进")
			}
		}

		// 不重叠、不遗漏：合并条数等于总数，且 ID 集合恰好为 1..n 各一次。
		if len(merged) != len(rows) {
			t.Fatalf("合并所有页应覆盖全部 %d 条，实际 %d 条", len(rows), len(merged))
		}
		seen := make(map[int64]int, len(merged))
		for _, rec := range merged {
			seen[rec.ID]++
		}
		for _, src := range rows {
			if seen[src.ID] != 1 {
				t.Fatalf("记录 ID=%d 应恰好出现一次，实际 %d 次", src.ID, seen[src.ID])
			}
		}

		// 整体倒序：相邻记录后者发生时间不晚于前者；同刻时 ID 不大于前者。
		for i := 1; i < len(merged); i++ {
			prev, cur := merged[i-1], merged[i]
			if cur.OccurredAt.After(prev.OccurredAt) {
				t.Fatalf("第 %d 条 %v 晚于第 %d 条 %v，违反倒序",
					i, cur.OccurredAt, i-1, prev.OccurredAt)
			}
			if cur.OccurredAt.Equal(prev.OccurredAt) && cur.ID > prev.ID {
				t.Fatalf("同刻记录未按 ID 倒序：第 %d 条 ID=%d 大于第 %d 条 ID=%d",
					i, cur.ID, i-1, prev.ID)
			}
		}
	})
}
