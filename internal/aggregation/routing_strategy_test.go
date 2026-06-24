package aggregation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

type routeRecordingInvoker struct {
	available     map[string]bool
	errByUpstream map[string]error
	returnResult  domain.ToolResult
	mu            sync.Mutex
	calls         []string
}

func (i *routeRecordingInvoker) UpstreamAvailable(upstreamID string) bool {
	if i.available == nil {
		return true
	}
	return i.available[upstreamID]
}

func (i *routeRecordingInvoker) CallUpstream(_ context.Context, upstreamID, originalName string, _ json.RawMessage) (domain.ToolResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.calls = append(i.calls, upstreamID+":"+originalName)
	if i.errByUpstream != nil {
		if err := i.errByUpstream[upstreamID]; err != nil {
			return domain.ToolResult{}, err
		}
	}
	if i.returnResult.Content != nil || i.returnResult.IsError {
		return i.returnResult, nil
	}
	return domain.ToolResult{Content: json.RawMessage(`[]`)}, nil
}

func (i *routeRecordingInvoker) callAt(idx int) string {
	i.mu.Lock()
	defer i.mu.Unlock()
	if idx >= len(i.calls) {
		return ""
	}
	return i.calls[idx]
}

func (i *routeRecordingInvoker) callCount() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.calls)
}

type memoryQuotaCounter struct {
	mu     sync.Mutex
	counts map[string]int64
}

func (c *memoryQuotaCounter) Reserve(_ context.Context, items []QuotaReservation) (bool, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = make(map[string]int64)
	}
	for i, item := range items {
		if c.counts[item.Key] >= int64(item.Limit) {
			return false, i + 1, nil
		}
	}
	for _, item := range items {
		c.counts[item.Key]++
	}
	return true, 0, nil
}

func (c *memoryQuotaCounter) totalForWindow(window string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var total int64
	needle := ":" + window + ":"
	for key, count := range c.counts {
		if strings.Contains(key, needle) {
			total += count
		}
	}
	return total
}

func routeEnabledUpstream(id string, sortOrder int, limits domain.UpstreamRateLimits) domain.Upstream {
	up := invEnabledUpstream(id, sortOrder)
	up.Config.RateLimits = limits
	return up
}

func routeService(tools map[string][]domain.ToolDef, upstreams []domain.Upstream, invoker *routeRecordingInvoker) *Service {
	cache := &invFakeCache{tools: tools}
	aliases := &invFakeAliases{byUpstream: map[string][]domain.AliasRule{}}
	mcpFilters := &invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}}
	apiKeyFilters := &invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{}}
	return invNewService(cache, &invFakeUpstreams{upstreams: upstreams}, aliases, mcpFilters, apiKeyFilters).SetInvoker(invoker)
}

func routeServiceWithPolicies(
	tools map[string][]domain.ToolDef,
	upstreams []domain.Upstream,
	invoker *routeRecordingInvoker,
	policies []domain.ToolPolicyRule,
) *Service {
	cache := &invFakeCache{tools: tools}
	aliases := &invFakeAliases{byUpstream: map[string][]domain.AliasRule{}}
	mcpFilters := &invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}}
	apiKeyFilters := &invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{}}
	return NewService(
		cache,
		domain.NewRuleEngine(),
		&invFakeUpstreams{upstreams: upstreams},
		aliases,
		mcpFilters,
		apiKeyFilters,
		&invFakeToolPolicies{rules: policies},
	).SetInvoker(invoker)
}

func TestPriorityFillSkipsUnavailableSource(t *testing.T) {
	invoker := &routeRecordingInvoker{available: map[string]bool{"up-a": false, "up-b": true}}
	svc := routeService(
		map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
			"up-b": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
		},
		[]domain.Upstream{invEnabledUpstream("up-a", 0), invEnabledUpstream("up-b", 1)},
		invoker,
	)

	if _, err := svc.InvokeTool(context.Background(), "", "read", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("优先填充应跳过不可用来源并调用可用来源：%v", err)
	}
	if got := invoker.callAt(0); got != "up-b:read" {
		t.Fatalf("应调用第二个可用来源，got=%q", got)
	}
}

func TestRoundRobinAlternatesCompatibleSources(t *testing.T) {
	invoker := &routeRecordingInvoker{available: map[string]bool{"up-a": true, "up-b": true}}
	svc := routeService(
		map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
			"up-b": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
		},
		[]domain.Upstream{invEnabledUpstream("up-a", 0), invEnabledUpstream("up-b", 1)},
		invoker,
	).SetRoutingStrategy(domain.ToolRoutingRoundRobin)

	for i := 0; i < 4; i++ {
		if _, err := svc.InvokeTool(context.Background(), "", "read", json.RawMessage(`{}`)); err != nil {
			t.Fatalf("第 %d 次轮询调用失败：%v", i+1, err)
		}
	}
	want := []string{"up-a:read", "up-b:read", "up-a:read", "up-b:read"}
	for i, expected := range want {
		if got := invoker.callAt(i); got != expected {
			t.Fatalf("第 %d 次调用路由错误：got=%q want=%q", i+1, got, expected)
		}
	}
}

func TestDefaultRoutingStrategyUsesSmartBalance(t *testing.T) {
	invoker := &routeRecordingInvoker{available: map[string]bool{"up-a": true, "up-b": true}}
	svc := routeService(
		map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
			"up-b": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
		},
		[]domain.Upstream{invEnabledUpstream("up-a", 0), invEnabledUpstream("up-b", 1)},
		invoker,
	)

	for i := 0; i < 2; i++ {
		if _, err := svc.InvokeTool(context.Background(), "", "read", json.RawMessage(`{}`)); err != nil {
			t.Fatalf("第 %d 次默认策略调用失败：%v", i+1, err)
		}
	}
	if got := invoker.callAt(0); got != "up-a:read" {
		t.Fatalf("默认策略首次调用路由错误：got=%q", got)
	}
	if got := invoker.callAt(1); got != "up-b:read" {
		t.Fatalf("默认策略应轮询到第二个来源：got=%q", got)
	}
}

func TestSmartBalanceAlternatesCompatibleSources(t *testing.T) {
	invoker := &routeRecordingInvoker{available: map[string]bool{"up-a": true, "up-b": true}}
	svc := routeService(
		map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
			"up-b": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
		},
		[]domain.Upstream{invEnabledUpstream("up-a", 0), invEnabledUpstream("up-b", 1)},
		invoker,
	).SetRoutingStrategy(domain.ToolRoutingSmartBalance)

	for i := 0; i < 4; i++ {
		if _, err := svc.InvokeTool(context.Background(), "", "read", json.RawMessage(`{}`)); err != nil {
			t.Fatalf("第 %d 次智能均衡调用失败：%v", i+1, err)
		}
	}
	want := []string{"up-a:read", "up-b:read", "up-a:read", "up-b:read"}
	for i, expected := range want {
		if got := invoker.callAt(i); got != expected {
			t.Fatalf("第 %d 次调用路由错误：got=%q want=%q", i+1, got, expected)
		}
	}
}

func TestToolPolicyOverridesRoutingStrategy(t *testing.T) {
	invoker := &routeRecordingInvoker{available: map[string]bool{"up-a": true, "up-b": true}}
	svc := routeServiceWithPolicies(
		map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
			"up-b": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
		},
		[]domain.Upstream{invEnabledUpstream("up-a", 0), invEnabledUpstream("up-b", 1)},
		invoker,
		[]domain.ToolPolicyRule{{
			Pattern:         "read",
			Enabled:         true,
			RoutingStrategy: domain.ToolRoutingPriorityFill,
		}},
	).SetRoutingStrategy(domain.ToolRoutingRoundRobin)

	for i := 0; i < 3; i++ {
		if _, err := svc.InvokeTool(context.Background(), "", "read", json.RawMessage(`{}`)); err != nil {
			t.Fatalf("第 %d 次策略路由调用失败：%v", i+1, err)
		}
	}
	for i := 0; i < 3; i++ {
		if got := invoker.callAt(i); got != "up-a:read" {
			t.Fatalf("工具策略应覆盖为优先顺序，第 %d 次 got=%q", i+1, got)
		}
	}
}

func TestToolPolicyCachesSuccessfulResult(t *testing.T) {
	invoker := &routeRecordingInvoker{available: map[string]bool{"up-a": true}}
	svc := routeServiceWithPolicies(
		map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
		},
		[]domain.Upstream{invEnabledUpstream("up-a", 0)},
		invoker,
		[]domain.ToolPolicyRule{{
			Pattern:         "read",
			Enabled:         true,
			CacheEnabled:    true,
			CacheTTLSeconds: 60,
		}},
	)

	for i := 0; i < 2; i++ {
		if _, err := svc.InvokeTool(context.Background(), "", "read", json.RawMessage(`{"q":1}`)); err != nil {
			t.Fatalf("第 %d 次缓存策略调用失败：%v", i+1, err)
		}
	}
	if got := invoker.callCount(); got != 1 {
		t.Fatalf("第二次相同参数应命中缓存，不应再次调用上游，got calls=%d", got)
	}

	if _, err := svc.InvokeTool(context.Background(), "", "read", json.RawMessage(`{"q":2}`)); err != nil {
		t.Fatalf("不同参数调用失败：%v", err)
	}
	if got := invoker.callCount(); got != 2 {
		t.Fatalf("不同参数不应复用缓存，got calls=%d", got)
	}
	stats := svc.ToolResultCacheStats()
	if stats.Hits != 1 || stats.Misses != 2 || stats.Stores != 2 || stats.Entries != 2 {
		t.Fatalf("缓存统计不符合预期：%+v", stats)
	}
}

func TestToolResultCacheStatsPrunesExpiredEntries(t *testing.T) {
	invoker := &routeRecordingInvoker{available: map[string]bool{"up-a": true}}
	svc := routeServiceWithPolicies(
		map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
		},
		[]domain.Upstream{invEnabledUpstream("up-a", 0)},
		invoker,
		[]domain.ToolPolicyRule{{
			Pattern:         "read",
			Enabled:         true,
			CacheEnabled:    true,
			CacheTTLSeconds: 1,
		}},
	)
	now := time.Date(2026, 6, 23, 10, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	if _, err := svc.InvokeTool(context.Background(), "", "read", json.RawMessage(`{"q":1}`)); err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	now = now.Add(2 * time.Second)

	stats := svc.ToolResultCacheStats()
	if stats.Entries != 0 || stats.Expired != 1 {
		t.Fatalf("expired entry should be pruned by stats read: %+v", stats)
	}
}

func TestClearToolResultCacheByTool(t *testing.T) {
	invoker := &routeRecordingInvoker{available: map[string]bool{"up-a": true}}
	svc := routeServiceWithPolicies(
		map[string][]domain.ToolDef{
			"up-a": {
				{OriginalName: "read", Name: "read", InputSchema: []byte("{}")},
				{OriginalName: "write", Name: "write", InputSchema: []byte("{}")},
			},
		},
		[]domain.Upstream{invEnabledUpstream("up-a", 0)},
		invoker,
		[]domain.ToolPolicyRule{{
			Pattern:         ".+",
			IsRegex:         true,
			Enabled:         true,
			CacheEnabled:    true,
			CacheTTLSeconds: 60,
		}},
	)

	if _, err := svc.InvokeTool(context.Background(), "", "read", json.RawMessage(`{"q":1}`)); err != nil {
		t.Fatalf("read call failed: %v", err)
	}
	if _, err := svc.InvokeTool(context.Background(), "", "write", json.RawMessage(`{"q":1}`)); err != nil {
		t.Fatalf("write call failed: %v", err)
	}

	result := svc.ClearToolResultCache(domain.ToolResultCacheClearFilter{ExposedName: "read"})
	if result.Deleted != 1 || result.Remaining != 1 {
		t.Fatalf("unexpected clear result: %+v", result)
	}
	stats := svc.ToolResultCacheStats()
	if stats.Entries != 1 || stats.LastClearedAt == nil {
		t.Fatalf("unexpected stats after clear: %+v", stats)
	}
}

func TestToolPolicySkipsOversizedCachedResult(t *testing.T) {
	largeContent := json.RawMessage(`[{"type":"text","text":"` + strings.Repeat("x", maxCachedToolResultBytes) + `"}]`)
	invoker := &routeRecordingInvoker{
		available:    map[string]bool{"up-a": true},
		returnResult: domain.ToolResult{Content: largeContent},
	}
	svc := routeServiceWithPolicies(
		map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
		},
		[]domain.Upstream{invEnabledUpstream("up-a", 0)},
		invoker,
		[]domain.ToolPolicyRule{{
			Pattern:         "read",
			Enabled:         true,
			CacheEnabled:    true,
			CacheTTLSeconds: 60,
		}},
	)

	for i := 0; i < 2; i++ {
		if _, err := svc.InvokeTool(context.Background(), "", "read", json.RawMessage(`{"q":1}`)); err != nil {
			t.Fatalf("第 %d 次超大结果调用失败：%v", i+1, err)
		}
	}
	if got := invoker.callCount(); got != 2 {
		t.Fatalf("超大结果不应写入短 TTL 缓存，第二次应重新调用上游，got calls=%d", got)
	}
}

func TestBuildToolDetailsIncludesMatchedToolPolicy(t *testing.T) {
	svc := routeServiceWithPolicies(
		map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "send_report", Name: "send_report", InputSchema: []byte("{}")}},
		},
		[]domain.Upstream{invEnabledUpstream("up-a", 0)},
		&routeRecordingInvoker{available: map[string]bool{"up-a": true}},
		[]domain.ToolPolicyRule{{
			ID:              "policy-1",
			Pattern:         "send_.+",
			IsRegex:         true,
			Enabled:         true,
			CacheEnabled:    true,
			CacheTTLSeconds: 30,
			RiskTags:        []string{"外发"},
			IgnoredRiskTags: []string{"send"},
		}},
	)

	details, err := svc.BuildToolDetails(context.Background(), "")
	if err != nil {
		t.Fatalf("构建工具详情失败：%v", err)
	}
	if len(details) != 1 || details[0].Policy == nil {
		t.Fatalf("应返回命中的工具策略：%+v", details)
	}
	if details[0].Policy.RuleID != "policy-1" || details[0].Policy.RiskTags[0] != "外发" || details[0].Policy.IgnoredRiskTags[0] != "send" {
		t.Fatalf("策略详情不符合预期：%+v", details[0].Policy)
	}
}

func TestSchemaConflictDoesNotRouteToIncompatibleSource(t *testing.T) {
	invoker := &routeRecordingInvoker{available: map[string]bool{"up-a": false, "up-b": true}}
	svc := routeService(
		map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "query", Name: "query", InputSchema: []byte(`{"type":"object"}`)}},
			"up-b": {{OriginalName: "query", Name: "query", InputSchema: []byte(`{"type":"string"}`)}},
		},
		[]domain.Upstream{invEnabledUpstream("up-a", 0), invEnabledUpstream("up-b", 1)},
		invoker,
	)

	_, err := svc.InvokeTool(context.Background(), "", "query", json.RawMessage(`{}`))
	var apiErr *domain.APIError
	if err == nil || !errors.As(err, &apiErr) || apiErr.Code != domain.CodeUpstreamUnavailable {
		t.Fatalf("展示 schema 不兼容的来源不应参与路由，got err=%v", err)
	}
	if invoker.callCount() != 0 {
		t.Fatalf("不应调用不兼容来源，calls=%v", invoker.calls)
	}
}

func TestQuotaExhaustionFallsThroughThenBlocks(t *testing.T) {
	limits := domain.UpstreamRateLimits{Enabled: true, PerMinute: 1, Timezone: "UTC"}
	invoker := &routeRecordingInvoker{available: map[string]bool{"up-a": true, "up-b": true}}
	svc := routeService(
		map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
			"up-b": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
		},
		[]domain.Upstream{routeEnabledUpstream("up-a", 0, limits), routeEnabledUpstream("up-b", 1, limits)},
		invoker,
	)
	q := NewQuotaManager(&memoryQuotaCounter{}, nil)
	q.now = func() time.Time { return time.Date(2026, 6, 23, 10, 30, 0, 0, time.UTC) }
	svc.SetQuotaManager(q)

	if _, err := svc.InvokeTool(context.Background(), "", "read", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("第一次调用应命中 up-a：%v", err)
	}
	if _, err := svc.InvokeTool(context.Background(), "", "read", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("up-a 超额后应切到 up-b：%v", err)
	}
	_, err := svc.InvokeTool(context.Background(), "", "read", json.RawMessage(`{}`))
	var apiErr *domain.APIError
	if err == nil || !errors.As(err, &apiErr) || apiErr.Code != domain.CodeRateLimited {
		t.Fatalf("两个来源都超额后应返回限流错误，got err=%v", err)
	}

	want := []string{"up-a:read", "up-b:read"}
	for i, expected := range want {
		if got := invoker.callAt(i); got != expected {
			t.Fatalf("第 %d 次调用路由错误：got=%q want=%q", i+1, got, expected)
		}
	}
	if invoker.callCount() != 2 {
		t.Fatalf("第三次限流不应转发，calls=%v", invoker.calls)
	}
}

func TestQuotaReservationDoesNotConsumeEarlierWindowsWhenLaterWindowIsExhausted(t *testing.T) {
	counter := &memoryQuotaCounter{}
	q := NewQuotaManager(counter, nil)
	q.now = func() time.Time { return time.Date(2026, 6, 23, 10, 30, 0, 0, time.UTC) }
	limits := domain.UpstreamRateLimits{
		Enabled:   true,
		PerMinute: 10,
		PerMonth:  1,
		Timezone:  "UTC",
	}

	allowed, reason := q.Allow(context.Background(), "up-a", limits)
	if !allowed {
		t.Fatalf("第一次额度预留应通过，reason=%q", reason)
	}
	allowed, reason = q.Allow(context.Background(), "up-a", limits)
	if allowed {
		t.Fatalf("第二次应因月度额度耗尽被拒绝")
	}
	if reason == "" {
		t.Fatalf("额度耗尽时应返回可读原因")
	}
	if got := counter.totalForWindow("minute"); got != 1 {
		t.Fatalf("月度额度已耗尽的拒绝请求不应消耗分钟额度，got=%d want=1", got)
	}
	if got := counter.totalForWindow("month"); got != 1 {
		t.Fatalf("拒绝请求不应继续增加月度额度，got=%d want=1", got)
	}
}

func TestSourceFailureCooldownSkipsRecentlyFailingSource(t *testing.T) {
	upstreamErr := domain.NewError(domain.CodeUpstreamUnavailable, "连接不可用")
	invoker := &routeRecordingInvoker{
		available:     map[string]bool{"up-a": true, "up-b": true},
		errByUpstream: map[string]error{"up-a": upstreamErr},
	}
	svc := routeService(
		map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "write", Name: "write", InputSchema: []byte("{}")}},
			"up-b": {{OriginalName: "write", Name: "write", InputSchema: []byte("{}")}},
		},
		[]domain.Upstream{invEnabledUpstream("up-a", 0), invEnabledUpstream("up-b", 1)},
		invoker,
	).SetRoutingStrategy(domain.ToolRoutingPriorityFill)
	base := time.Date(2026, 6, 23, 10, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return base }

	for i := 0; i < defaultSourceFailureThreshold; i++ {
		_, err := svc.InvokeTool(context.Background(), "", "write", json.RawMessage(`{}`))
		if !errors.Is(err, upstreamErr) {
			t.Fatalf("第 %d 次失败应直接返回 up-a 错误，got=%v", i+1, err)
		}
	}

	if _, err := svc.InvokeTool(context.Background(), "", "write", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("up-a 连续失败后应短暂降级到 up-b，got err=%v", err)
	}

	want := []string{"up-a:write", "up-a:write", "up-a:write", "up-b:write"}
	for i, expected := range want {
		if got := invoker.callAt(i); got != expected {
			t.Fatalf("第 %d 次调用来源错误：got=%q want=%q", i+1, got, expected)
		}
	}
}

func TestBuildToolDetailsMarksTemporarilyDegradedSource(t *testing.T) {
	upstreamErr := domain.NewError(domain.CodeUpstreamUnavailable, "连接不可用")
	invoker := &routeRecordingInvoker{
		available:     map[string]bool{"up-a": true, "up-b": true},
		errByUpstream: map[string]error{"up-a": upstreamErr},
	}
	svc := routeService(
		map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "write", Name: "write", InputSchema: []byte("{}")}},
			"up-b": {{OriginalName: "write", Name: "write", InputSchema: []byte("{}")}},
		},
		[]domain.Upstream{invEnabledUpstream("up-a", 0), invEnabledUpstream("up-b", 1)},
		invoker,
	).SetRoutingStrategy(domain.ToolRoutingPriorityFill)
	base := time.Date(2026, 6, 23, 10, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return base }

	for i := 0; i < defaultSourceFailureThreshold; i++ {
		_, _ = svc.InvokeTool(context.Background(), "", "write", json.RawMessage(`{}`))
	}

	details, err := svc.BuildToolDetails(context.Background(), "")
	if err != nil {
		t.Fatalf("构建工具详情不应失败：%v", err)
	}
	if len(details) != 1 {
		t.Fatalf("应返回 1 个工具详情，got=%d", len(details))
	}

	sources := map[string]domain.ToolSourceView{}
	for _, source := range details[0].Sources {
		sources[source.UpstreamID] = source
	}
	if !sources["up-a"].TemporarilyDegraded {
		t.Fatalf("连续失败来源应标记为临时降级：%+v", sources["up-a"])
	}
	if sources["up-a"].RoutingAvailable {
		t.Fatalf("临时降级来源在多来源工具中不应继续标记为可参与路由：%+v", sources["up-a"])
	}
	if sources["up-a"].DegradationReason == "" || sources["up-a"].DegradationUntil == nil {
		t.Fatalf("临时降级来源应带有可读原因和结束时间：%+v", sources["up-a"])
	}
	if !sources["up-b"].RoutingAvailable || sources["up-b"].TemporarilyDegraded {
		t.Fatalf("健康来源应保持可路由且不被标记降级：%+v", sources["up-b"])
	}
}

func TestBuildToolDetailsDoesNotMarkIncompatibleSourceAsDegraded(t *testing.T) {
	upstreamErr := domain.NewError(domain.CodeUpstreamUnavailable, "连接不可用")
	svc := routeService(
		map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "query", Name: "query", InputSchema: []byte(`{"type":"object"}`)}},
			"up-b": {{OriginalName: "query", Name: "query", InputSchema: []byte(`{"type":"string"}`)}},
		},
		[]domain.Upstream{invEnabledUpstream("up-a", 0), invEnabledUpstream("up-b", 1)},
		&routeRecordingInvoker{available: map[string]bool{"up-a": true, "up-b": true}},
	)
	svc.recordSourceResult(ToolCandidate{
		UpstreamID:   "up-b",
		OriginalName: "query",
		Compatible:   false,
	}, upstreamErr)
	svc.recordSourceResult(ToolCandidate{
		UpstreamID:   "up-b",
		OriginalName: "query",
		Compatible:   false,
	}, upstreamErr)
	svc.recordSourceResult(ToolCandidate{
		UpstreamID:   "up-b",
		OriginalName: "query",
		Compatible:   false,
	}, upstreamErr)

	details, err := svc.BuildToolDetails(context.Background(), "")
	if err != nil {
		t.Fatalf("构建工具详情不应失败：%v", err)
	}
	if len(details) != 1 {
		t.Fatalf("应返回 1 个工具详情，got=%d", len(details))
	}
	for _, source := range details[0].Sources {
		if source.UpstreamID == "up-b" && source.TemporarilyDegraded {
			t.Fatalf("Schema 不兼容来源本来不参与路由，不应叠加临时降级状态：%+v", source)
		}
	}
}

func TestSourceFailureCooldownExpiresAndRetriesOriginalSource(t *testing.T) {
	upstreamErr := domain.NewError(domain.CodeUpstreamUnavailable, "连接不可用")
	invoker := &routeRecordingInvoker{
		available:     map[string]bool{"up-a": true, "up-b": true},
		errByUpstream: map[string]error{"up-a": upstreamErr},
	}
	svc := routeService(
		map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
			"up-b": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
		},
		[]domain.Upstream{invEnabledUpstream("up-a", 0), invEnabledUpstream("up-b", 1)},
		invoker,
	).SetRoutingStrategy(domain.ToolRoutingPriorityFill)
	base := time.Date(2026, 6, 23, 10, 30, 0, 0, time.UTC)
	now := base
	svc.now = func() time.Time { return now }

	for i := 0; i < defaultSourceFailureThreshold; i++ {
		_, _ = svc.InvokeTool(context.Background(), "", "read", json.RawMessage(`{}`))
	}
	now = base.Add(defaultSourceFailureCooldown + time.Second)

	_, err := svc.InvokeTool(context.Background(), "", "read", json.RawMessage(`{}`))
	if !errors.Is(err, upstreamErr) {
		t.Fatalf("冷却过期后应重新尝试原优先来源，got=%v", err)
	}
	if got := invoker.callAt(defaultSourceFailureThreshold); got != "up-a:read" {
		t.Fatalf("冷却过期后调用来源错误：got=%q want=%q", got, "up-a:read")
	}
}

func TestSourceFailureDoesNotHideSingleSourceTool(t *testing.T) {
	upstreamErr := domain.NewError(domain.CodeUpstreamUnavailable, "连接不可用")
	invoker := &routeRecordingInvoker{
		available:     map[string]bool{"up-a": true},
		errByUpstream: map[string]error{"up-a": upstreamErr},
	}
	svc := routeService(
		map[string][]domain.ToolDef{
			"up-a": {{OriginalName: "read", Name: "read", InputSchema: []byte("{}")}},
		},
		[]domain.Upstream{invEnabledUpstream("up-a", 0)},
		invoker,
	).SetRoutingStrategy(domain.ToolRoutingPriorityFill)
	svc.now = func() time.Time { return time.Date(2026, 6, 23, 10, 30, 0, 0, time.UTC) }

	for i := 0; i < defaultSourceFailureThreshold+1; i++ {
		_, err := svc.InvokeTool(context.Background(), "", "read", json.RawMessage(`{}`))
		if !errors.Is(err, upstreamErr) {
			t.Fatalf("单来源工具不应因降级状态被隐藏，第 %d 次 got=%v", i+1, err)
		}
	}
	if got := invoker.callCount(); got != defaultSourceFailureThreshold+1 {
		t.Fatalf("单来源工具每次都应尝试真实来源，got calls=%d", got)
	}
}
