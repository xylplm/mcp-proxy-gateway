package stats

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/store"
	"pgregory.net/rapid"
)

// rankingFakeQuerier 是 StatQuerier 的内存实现，用于属性测试中忠实模拟仓储层
// TopTools 的 SQL 语义（Req 16.3）：
//   - 基于稳定标识 (upstream_id, original_name) 聚合的若干工具计数项；
//   - 按调用次数降序排列（同次数以 original_name 升序作稳定 tie-break，与仓储
//     `ORDER BY c DESC, original_name ASC` 一致）；
//   - 至多返回收敛后的 limit 条（limit ≤ 0 时按 1 处理）。
//
// CountByUpstream / CountByAPIKey 非本属性关注点，返回空切片即可。
type rankingFakeQuerier struct {
	tools []store.ToolRank
}

func (q *rankingFakeQuerier) CountByUpstream(context.Context, time.Time, time.Time) ([]store.DimensionCount, error) {
	return nil, nil
}

func (q *rankingFakeQuerier) CountByAPIKey(context.Context, time.Time, time.Time) ([]store.DimensionCount, error) {
	return nil, nil
}

func (q *rankingFakeQuerier) TopTools(_ context.Context, _, _ time.Time, limit int) ([]store.ToolRank, error) {
	if limit <= 0 {
		limit = 1
	}
	sorted := append([]store.ToolRank(nil), q.tools...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Count != sorted[j].Count {
			return sorted[i].Count > sorted[j].Count
		}
		return sorted[i].OriginalName < sorted[j].OriginalName
	})
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	return sorted, nil
}

func (q *rankingFakeQuerier) Summary(context.Context, time.Time, time.Time) (store.StatsSummary, error) {
	return store.StatsSummary{}, nil
}

func (q *rankingFakeQuerier) Daily(context.Context, time.Time, time.Time, string) ([]store.DailyCount, error) {
	return nil, nil
}

func (q *rankingFakeQuerier) TopToolErrors(context.Context, time.Time, time.Time, int) ([]store.ToolErrorRank, error) {
	return nil, nil
}

func (q *rankingFakeQuerier) APIKeyUsageProfile(context.Context, string, time.Time, time.Time, int) (store.APIKeyUsageProfile, error) {
	return store.APIKeyUsageProfile{}, nil
}

func (q *rankingFakeQuerier) ListRecords(context.Context, store.CallRecordQuery) ([]store.CallRecordView, error) {
	return nil, nil
}

func (q *rankingFakeQuerier) HealthRecords(context.Context, time.Time, time.Time, int) ([]store.CallRecordView, error) {
	return nil, nil
}

func (q *rankingFakeQuerier) GetRecord(context.Context, int64) (store.CallRecordView, error) {
	return store.CallRecordView{}, nil
}

func (q *rankingFakeQuerier) ClearRecordsBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}

// genToolRanks 生成一组按 (upstream_id, original_name) 稳定标识唯一的工具计数项。
//
// 通过对每条记录使用自增序号构造唯一稳定标识，避免聚合重复；调用次数取非负随机值
// （含 0 与相同次数，用于覆盖降序排列中的同分 tie-break）。
func genToolRanks() *rapid.Generator[[]store.ToolRank] {
	return rapid.Custom(func(t *rapid.T) []store.ToolRank {
		n := rapid.IntRange(0, 60).Draw(t, "工具数")
		tools := make([]store.ToolRank, 0, n)
		for i := range n {
			upstream := "u" + rapid.StringMatching(`[0-9]{1,3}`).Draw(t, "上游序号")
			name := "tool-" + rapid.StringN(1, 6, 6).Draw(t, "工具名")
			count := int64(rapid.IntRange(0, 100000).Draw(t, "调用次数"))
			tools = append(tools, store.ToolRank{
				// 拼接自增序号保证稳定标识唯一，避免同标识聚合歧义。
				UpstreamID:   upstream + "-" + itoa(i),
				OriginalName: name + "-" + itoa(i),
				Count:        count,
			})
		}
		return tools
	})
}

// itoa 为不依赖 strconv 的极简非负整数转字符串，仅用于构造唯一标识。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// expectedTopLimit 复刻 QueryService.resolveTopLimit + defaultTopLimit 的收敛逻辑，
// 作为属性测试的独立 oracle（Req 16.3）：
//   - requested ≤ 0 → 取配置默认值；配置默认越界（<1 或 >100）回退硬编码默认 10；
//   - requested > 100 → 收敛为 100；否则原样。
func expectedTopLimit(requested, cfgDefault int) int {
	if requested <= 0 {
		if cfgDefault < minTopLimit || cfgDefault > maxTopLimit {
			return defaultTopLimit
		}
		return cfgDefault
	}
	if requested > maxTopLimit {
		return maxTopLimit
	}
	return requested
}

// Feature: mcp-proxy-gateway, Property 23: 工具排行降序且条数受限
//
// Validates: Requirements 16.3
//
// 对任意工具调用计数数据与配置的返回条数（默认 10，范围 1-100），验证 QueryService.TopTools：
//   - 结果按调用次数降序（非递增）排列；
//   - 返回条数不超过生效的配置上限（覆盖 requested ≤0 取默认、范围内、超上界收敛，
//     以及配置默认越界回退默认 10）；
//   - 生效上限始终收敛到 [1, 100]。
func TestProperty23ToolRankingDescendingBoundedCount(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		tools := genToolRanks().Draw(t, "工具计数数据")

		// 请求条数覆盖：非正（触发默认回退）、范围内 1-100、超上界（触发收敛）。
		requested := rapid.OneOf(
			rapid.IntRange(-5, 0),
			rapid.IntRange(minTopLimit, maxTopLimit),
			rapid.IntRange(maxTopLimit+1, maxTopLimit+200),
		).Draw(t, "请求条数")
		// 配置默认条数：含越界值，验证回退到硬编码默认 10。
		cfgDefault := rapid.IntRange(-3, maxTopLimit+20).Draw(t, "配置默认条数")

		repo := &rankingFakeQuerier{tools: append([]store.ToolRank{}, tools...)}
		svc, err := NewQueryService(repo, fakeQueryCfg{topLimitDefault: cfgDefault})
		if err != nil {
			t.Fatalf("NewQueryService 不应返回错误：%v", err)
		}

		start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC)
		got, err := svc.TopTools(context.Background(), start, end, requested)
		if err != nil {
			t.Fatalf("TopTools 不应返回错误：%v", err)
		}

		effLimit := expectedTopLimit(requested, cfgDefault)
		// 生效上限必须落在合法区间 [1, 100]。
		if effLimit < minTopLimit || effLimit > maxTopLimit {
			t.Fatalf("生效上限 %d 应收敛到 [%d, %d]", effLimit, minTopLimit, maxTopLimit)
		}

		// 属性一：返回条数不超过生效上限。
		if len(got) > effLimit {
			t.Fatalf("返回条数 %d 超过生效上限 %d（requested=%d cfgDefault=%d）",
				len(got), effLimit, requested, cfgDefault)
		}
		// 返回条数也不应超过可用工具总数。
		if len(got) > len(tools) {
			t.Fatalf("返回条数 %d 超过可用工具总数 %d", len(got), len(tools))
		}

		// 属性二：结果按调用次数降序（非递增）排列。
		for i := 1; i < len(got); i++ {
			if got[i].Count > got[i-1].Count {
				t.Fatalf("第 %d 条次数 %d 大于第 %d 条次数 %d，违反降序",
					i, got[i].Count, i-1, got[i-1].Count)
			}
		}
	})
}
