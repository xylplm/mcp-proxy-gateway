package aggregation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件为任务 4.6「InvokeTool 路由与别名反向映射」的单元测试，验证以下核心功能逻辑：
//   - 工具不在可见聚合集合内时返回 TOOL_NOT_FOUND 且不向任何上游转发（Req 10.4、11.7）；
//   - 命中时按反向映射唯一还原 (上游ID, 原始名)，并以原始参数透传转发（Req 10.3、10.6）；
//   - 被别名重写改名的工具，按其对外名调用时仍能反向映射回上游原始名（Req 10.6）；
//   - 被 API Key 级屏蔽规则过滤而不可见的工具，调用时返回 TOOL_NOT_FOUND（Req 11.7、13.7）；
//   - 未注入真实上游会话（invoker 为 nil）时，通过可见性校验后返回占位错误，
//     可见性校验逻辑仍完整执行（本任务以接口占位、不接入真实上游会话）。
//
// 文件内的内存假实现统一使用 inv 前缀命名，避免与同包内其它测试标识符冲突。

// invFakeCache 是 domain.Tool_Cache 的内存假实现，按上游标识返回预置的工具列表。
type invFakeCache struct {
	tools map[string][]domain.ToolDef
}

func (c *invFakeCache) Get(_ context.Context, upstreamID string) ([]domain.ToolDef, time.Time, bool) {
	tools, ok := c.tools[upstreamID]
	return tools, time.Time{}, ok
}

func (c *invFakeCache) Replace(_ context.Context, _ string, _ []domain.ToolDef) error { return nil }

func (c *invFakeCache) Delete(_ context.Context, _ string) error { return nil }

// invFakeUpstreams 是 UpstreamLister 的内存假实现。
type invFakeUpstreams struct {
	upstreams []domain.Upstream
}

func (u *invFakeUpstreams) ListUpstreams(_ context.Context) ([]domain.Upstream, error) {
	return u.upstreams, nil
}

// invFakeAliases 是 AliasLister 的内存假实现。
type invFakeAliases struct {
	byUpstream map[string][]domain.AliasRule
}

func (a *invFakeAliases) ListAliasesByUpstream(_ context.Context, upstreamID string) ([]domain.AliasRule, error) {
	return a.byUpstream[upstreamID], nil
}

// invFakeMCPFilters 是 MCPFilterLister 的内存假实现。
type invFakeMCPFilters struct {
	byUpstream map[string][]domain.FilterRule
}

func (f *invFakeMCPFilters) ListMCPFiltersByUpstream(_ context.Context, upstreamID string) ([]domain.FilterRule, error) {
	return f.byUpstream[upstreamID], nil
}

// invFakeAPIKeyFilters 是 APIKeyFilterLister 的内存假实现。
type invFakeAPIKeyFilters struct {
	byAPIKey map[string][]domain.FilterRule
}

func (f *invFakeAPIKeyFilters) ListAPIKeyFiltersByAPIKey(_ context.Context, apiKeyID string) ([]domain.FilterRule, error) {
	return f.byAPIKey[apiKeyID], nil
}

// invRecordingInvoker 是 UpstreamInvoker 的内存假实现，记录最近一次转发调用的参数，
// 并可返回预置结果，用于断言「以原始参数透传」与「结果原样返回」。
type invRecordingInvoker struct {
	called       bool
	gotUpstream  string
	gotOriginal  string
	gotArgs      json.RawMessage
	returnResult domain.ToolResult
	returnErr    error
}

func (i *invRecordingInvoker) CallUpstream(_ context.Context, upstreamID, originalName string, args json.RawMessage) (domain.ToolResult, error) {
	i.called = true
	i.gotUpstream = upstreamID
	i.gotOriginal = originalName
	i.gotArgs = args
	return i.returnResult, i.returnErr
}

// invEnabledUpstream 构造一个启用上游。
func invEnabledUpstream(id string, sortOrder int) domain.Upstream {
	return domain.Upstream{
		ID: id,
		Config: domain.UpstreamConfig{
			Name:      id,
			Enabled:   true,
			SortOrder: sortOrder,
		},
		State: domain.ConnAvailable,
	}
}

// invNewService 用给定的内存假实现组装一个 *Service（规则引擎使用真实实现）。
func invNewService(
	cache domain.Tool_Cache,
	upstreams UpstreamLister,
	aliases AliasLister,
	mcpFilters MCPFilterLister,
	apiKeyFilters APIKeyFilterLister,
) *Service {
	return NewService(cache, domain.NewRuleEngine(), upstreams, aliases, mcpFilters, apiKeyFilters)
}

// TestInvokeToolHitForwardsWithOriginalName 验证：命中可见集合时，InvokeTool 按反向映射
// 还原上游原始名，并以原始参数透传转发，且上游结果原样返回（Req 10.3、10.6）。
func TestInvokeToolHitForwardsWithOriginalName(t *testing.T) {
	cache := &invFakeCache{tools: map[string][]domain.ToolDef{
		"up-a": {{OriginalName: "read_file", Name: "read_file"}},
	}}
	upstreams := &invFakeUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up-a", 0)}}
	aliases := &invFakeAliases{byUpstream: map[string][]domain.AliasRule{}}
	mcpFilters := &invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}}
	apiKeyFilters := &invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{}}

	wantResult := domain.ToolResult{IsError: false, Content: json.RawMessage(`[{"type":"text","text":"ok"}]`)}
	invoker := &invRecordingInvoker{returnResult: wantResult}

	svc := invNewService(cache, upstreams, aliases, mcpFilters, apiKeyFilters).SetInvoker(invoker)

	args := json.RawMessage(`{"path":"/tmp/a.txt"}`)
	got, err := svc.InvokeTool(context.Background(), "", "read_file", args)
	if err != nil {
		t.Fatalf("命中可见工具时不应返回错误，got err=%v", err)
	}
	if !invoker.called {
		t.Fatalf("命中可见工具时应转发到上游，但 invoker 未被调用")
	}
	if invoker.gotUpstream != "up-a" {
		t.Fatalf("转发上游标识错误：got=%q want=%q", invoker.gotUpstream, "up-a")
	}
	if invoker.gotOriginal != "read_file" {
		t.Fatalf("转发上游原始名错误：got=%q want=%q", invoker.gotOriginal, "read_file")
	}
	if string(invoker.gotArgs) != string(args) {
		t.Fatalf("原始参数未原样透传：got=%s want=%s", invoker.gotArgs, args)
	}
	if got.IsError != wantResult.IsError || string(got.Content) != string(wantResult.Content) {
		t.Fatalf("上游结果未原样返回：got=%+v want=%+v", got, wantResult)
	}
}

// TestInvokeToolAliasReverseMapping 验证：被别名重写改名的工具，按其对外名调用时
// 仍能反向映射回上游原始名并转发（Req 10.6）。
func TestInvokeToolAliasReverseMapping(t *testing.T) {
	cache := &invFakeCache{tools: map[string][]domain.ToolDef{
		"up-a": {{OriginalName: "read_file", Name: "read_file"}},
	}}
	upstreams := &invFakeUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up-a", 0)}}
	aliases := &invFakeAliases{byUpstream: map[string][]domain.AliasRule{
		"up-a": {{Pattern: "read_file", IsRegex: false, TargetName: "fs_read", SortOrder: 0}},
	}}
	mcpFilters := &invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}}
	apiKeyFilters := &invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{}}

	invoker := &invRecordingInvoker{returnResult: domain.ToolResult{Content: json.RawMessage(`[]`)}}
	svc := invNewService(cache, upstreams, aliases, mcpFilters, apiKeyFilters).SetInvoker(invoker)

	// 按对外别名 "fs_read" 调用，应反向映射回原始名 "read_file"。
	if _, err := svc.InvokeTool(context.Background(), "", "fs_read", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("按对外别名调用不应返回错误，got err=%v", err)
	}
	if invoker.gotOriginal != "read_file" {
		t.Fatalf("别名反向映射错误：got original=%q want=%q", invoker.gotOriginal, "read_file")
	}

	// 按已不存在的原始名 "read_file" 调用（已被别名改名），应不可见而被拒。
	invoker.called = false
	_, err := svc.InvokeTool(context.Background(), "", "read_file", json.RawMessage(`{}`))
	assertToolNotFound(t, err)
	if invoker.called {
		t.Fatalf("调用已被改名的原始名不应转发到上游")
	}
}

// TestInvokeToolNotVisibleRejected 验证：不在可见集合内的工具名调用返回 TOOL_NOT_FOUND
// 且不向任何上游转发（Req 10.4、11.7）。
func TestInvokeToolNotVisibleRejected(t *testing.T) {
	cache := &invFakeCache{tools: map[string][]domain.ToolDef{
		"up-a": {{OriginalName: "read_file", Name: "read_file"}},
	}}
	upstreams := &invFakeUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up-a", 0)}}
	aliases := &invFakeAliases{byUpstream: map[string][]domain.AliasRule{}}
	mcpFilters := &invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}}
	apiKeyFilters := &invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{}}

	invoker := &invRecordingInvoker{}
	svc := invNewService(cache, upstreams, aliases, mcpFilters, apiKeyFilters).SetInvoker(invoker)

	_, err := svc.InvokeTool(context.Background(), "", "does_not_exist", json.RawMessage(`{}`))
	assertToolNotFound(t, err)
	if invoker.called {
		t.Fatalf("调用不可见工具不应转发到任何上游")
	}
}

// TestInvokeToolFilteredByAPIKeyRejected 验证：被 API Key 级屏蔽规则过滤而不可见的工具，
// 调用时返回 TOOL_NOT_FOUND 且不转发（Req 11.7、13.7）。
func TestInvokeToolFilteredByAPIKeyRejected(t *testing.T) {
	cache := &invFakeCache{tools: map[string][]domain.ToolDef{
		"up-a": {{OriginalName: "read_file", Name: "read_file"}},
	}}
	upstreams := &invFakeUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up-a", 0)}}
	aliases := &invFakeAliases{byUpstream: map[string][]domain.AliasRule{}}
	mcpFilters := &invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}}
	// 该 API Key 启用一条屏蔽规则，命中 read_file（匹配 OriginalName）。
	apiKeyFilters := &invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{
		"key-1": {{Pattern: "read_file", IsRegex: false, Enabled: true}},
	}}

	invoker := &invRecordingInvoker{}
	svc := invNewService(cache, upstreams, aliases, mcpFilters, apiKeyFilters).SetInvoker(invoker)

	// 无 API Key 视角下工具可见。
	if _, err := svc.InvokeTool(context.Background(), "", "read_file", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("无 API Key 视角下工具应可见，got err=%v", err)
	}

	// 该 API Key 视角下工具被过滤而不可见，调用应被拒且不转发。
	invoker.called = false
	_, err := svc.InvokeTool(context.Background(), "key-1", "read_file", json.RawMessage(`{}`))
	assertToolNotFound(t, err)
	if invoker.called {
		t.Fatalf("调用被 API Key 屏蔽的工具不应转发到上游")
	}
}

// TestInvokeToolNilInvokerPlaceholder 验证：未注入真实上游会话（invoker 为 nil）时，
// 通过可见性校验后返回占位错误；而不可见工具仍优先返回 TOOL_NOT_FOUND——即可见性校验
// 始终在转发占位之前完整执行。
func TestInvokeToolNilInvokerPlaceholder(t *testing.T) {
	cache := &invFakeCache{tools: map[string][]domain.ToolDef{
		"up-a": {{OriginalName: "read_file", Name: "read_file"}},
	}}
	upstreams := &invFakeUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up-a", 0)}}
	aliases := &invFakeAliases{byUpstream: map[string][]domain.AliasRule{}}
	mcpFilters := &invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}}
	apiKeyFilters := &invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{}}

	// 不调用 SetInvoker，invoker 保持 nil（模拟未接线装配的防御性兜底）。
	svc := invNewService(cache, upstreams, aliases, mcpFilters, apiKeyFilters)

	// 命中可见工具：通过可见性校验后因未接线上游会话返回占位错误。
	_, err := svc.InvokeTool(context.Background(), "", "read_file", json.RawMessage(`{}`))
	if !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("命中可见工具但 invoker 未注入时应返回占位错误，got err=%v", err)
	}

	// 不可见工具：在转发占位之前即被可见性校验拒绝，返回 TOOL_NOT_FOUND。
	_, err = svc.InvokeTool(context.Background(), "", "does_not_exist", json.RawMessage(`{}`))
	assertToolNotFound(t, err)
}

// assertToolNotFound 断言 err 是错误码为 TOOL_NOT_FOUND 的 *domain.APIError。
func assertToolNotFound(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望返回 TOOL_NOT_FOUND 错误，但 err 为 nil")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望 *domain.APIError，got %T: %v", err, err)
	}
	if apiErr.Code != domain.CodeToolNotFound {
		t.Fatalf("期望错误码 %s，got %s", domain.CodeToolNotFound, apiErr.Code)
	}
}
