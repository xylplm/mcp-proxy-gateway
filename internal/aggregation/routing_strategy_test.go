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
	available map[string]bool
	mu        sync.Mutex
	calls     []string
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

func TestDefaultRoutingStrategyIsRoundRobin(t *testing.T) {
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
