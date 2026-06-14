package aggregation

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件为任务 11.2「编写聚合调用路由集成测试」，验证聚合调用路由的端到端行为
// （Req 10.3、10.5、10.8）。
//
// 与 invoker_test.go（仅验证 Service 的可见性校验 + 反向映射，invoker 为内存记录桩）
// 及 upstream_invoker_test.go（仅单测 SessionInvoker 的调用语义）不同，本文件把两层
// 组合为完整链路：
//
//	Service（六阶段聚合管线 + 可见性校验 + 别名反向映射）
//	  → SessionInvoker（真实上游调用语义：连接状态判定 / 超时控制 / 结果原样透传）
//	    → ToolCaller（按上游标识获取的会话）
//
// 即外部调用方提供「对外名 + 原始参数」，经聚合服务还原为 (上游标识, 原始名) 后由真实
// 上游调用转发器执行，端到端覆盖：
//   - 连接不可用（GetState 非 available）→ UPSTREAM_UNAVAILABLE 且不触达上游会话（Req 10.5）；
//   - 上游调用超时 → UPSTREAM_TIMEOUT，不返回部分结果（Req 10.8）；
//   - 成功结果原样透传（Req 10.3）；
//   - 上游报告的错误结果（IsError=true）原样透传（Req 10.3）。
//
// 数据访问侧复用 invoker_test.go 中已有的内存假实现（invFakeCache / invFakeUpstreams /
// invFakeAliases / invFakeMCPFilters / invFakeAPIKeyFilters、invEnabledUpstream）与断言
// 助手（assertToolNotFound / assertUpstreamErrorCode）。本文件仅为「连接状态 / 会话」侧
// 新增 ir 前缀的内存假实现（支持按上游标识区分），避免与 si/inv 前缀标识符重复定义。

// irFakeStates 是 ConnStateProvider 的内存假实现，按上游标识返回预置的连接状态与失败原因。
//
// 与 upstream_invoker_test.go 中只返回单一状态的 siFakeStates 不同，本实现以 map 区分各
// 上游，便于集成测试模拟「部分上游不可用」等场景；未登记的上游按不可用处理。
type irFakeStates struct {
	states map[string]domain.ConnState
	errs   map[string]string
}

func (s *irFakeStates) GetState(upstreamID string) (domain.ConnState, string) {
	st, ok := s.states[upstreamID]
	if !ok {
		return domain.ConnUnavailable, "未登记的上游"
	}
	return st, s.errs[upstreamID]
}

// irFakeCaller 是 ToolCaller 的内存假实现，记录最近一次转发调用的原始名与参数，并按预置
// 行为返回。delay > 0 时模拟一个耗时调用：在 delay 内若 ctx 被取消（如超时）则返回
// ctx.Err()，用于驱动 SessionInvoker 的超时分支。
type irFakeCaller struct {
	called  atomic.Bool
	gotName string
	gotArgs json.RawMessage
	delay   time.Duration
	result  domain.ToolResult
	err     error
}

func (c *irFakeCaller) CallTool(ctx context.Context, name string, args json.RawMessage) (domain.ToolResult, error) {
	c.called.Store(true)
	c.gotName = name
	c.gotArgs = args
	if c.delay > 0 {
		select {
		case <-time.After(c.delay):
		case <-ctx.Done():
			return domain.ToolResult{}, ctx.Err()
		}
	}
	return c.result, c.err
}

// irFakeSessions 是 SessionProvider 的内存假实现，按上游标识返回对应会话；未登记返回 false。
type irFakeSessions struct {
	sessions map[string]ToolCaller
}

func (s *irFakeSessions) Session(upstreamID string) (ToolCaller, bool) {
	sess, ok := s.sessions[upstreamID]
	return sess, ok
}

// irBuildService 把数据访问侧与真实上游调用转发器接线为完整链路：
// Service（真实规则引擎 + 六阶段管线）注入 SessionInvoker（真实连接判定 + 超时控制）。
func irBuildService(
	cache domain.Tool_Cache,
	upstreams UpstreamLister,
	aliases AliasLister,
	mcpFilters MCPFilterLister,
	apiKeyFilters APIKeyFilterLister,
	states ConnStateProvider,
	sessions SessionProvider,
	callTimeout time.Duration,
) *Service {
	invoker := NewSessionInvoker(states, sessions, callTimeout, nil)
	return NewService(cache, domain.NewRuleEngine(), upstreams, aliases, mcpFilters, apiKeyFilters).SetInvoker(invoker)
}

// TestInvokeRoutingUnavailableUpstream 验证端到端：被调用工具所属上游连接不可用时，
// 聚合调用返回 UPSTREAM_UNAVAILABLE，且不向上游会话转发（Req 10.5）。
func TestInvokeRoutingUnavailableUpstream(t *testing.T) {
	for _, state := range []domain.ConnState{
		domain.ConnConnecting,
		domain.ConnUnavailable,
		domain.ConnSuspended,
	} {
		t.Run(string(state), func(t *testing.T) {
			cache := &invFakeCache{tools: map[string][]domain.ToolDef{
				"up-a": {{OriginalName: "read_file", Name: "read_file"}},
			}}
			upstreams := &invFakeUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up-a", 0)}}
			aliases := &invFakeAliases{byUpstream: map[string][]domain.AliasRule{}}
			mcpFilters := &invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}}
			apiKeyFilters := &invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{}}

			caller := &irFakeCaller{result: domain.ToolResult{Content: json.RawMessage(`[]`)}}
			states := &irFakeStates{
				states: map[string]domain.ConnState{"up-a": state},
				errs:   map[string]string{"up-a": "拨号失败"},
			}
			sessions := &irFakeSessions{sessions: map[string]ToolCaller{"up-a": caller}}

			svc := irBuildService(cache, upstreams, aliases, mcpFilters, apiKeyFilters, states, sessions, 30*time.Second)

			// 工具在可见集合内（通过可见性校验），但其上游连接不可用，应在转发前被拦截。
			_, err := svc.InvokeTool(context.Background(), "", "read_file", json.RawMessage(`{}`))
			assertUpstreamErrorCode(t, err, domain.CodeUpstreamUnavailable)
			if caller.called.Load() {
				t.Fatalf("上游连接不可用时不应向上游会话转发调用")
			}
		})
	}
}

// TestInvokeRoutingTimeout 验证端到端：上游在调用超时时长内未返回时，聚合调用中止本次
// 调用、不返回部分结果并返回 UPSTREAM_TIMEOUT（Req 10.8）。
func TestInvokeRoutingTimeout(t *testing.T) {
	cache := &invFakeCache{tools: map[string][]domain.ToolDef{
		"up-a": {{OriginalName: "slow_tool", Name: "slow_tool"}},
	}}
	upstreams := &invFakeUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up-a", 0)}}
	aliases := &invFakeAliases{byUpstream: map[string][]domain.AliasRule{}}
	mcpFilters := &invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}}
	apiKeyFilters := &invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{}}

	// 会话耗时远超超时时长，触发超时分支；其预置「迟到」结果不应被返回。
	caller := &irFakeCaller{
		delay:  500 * time.Millisecond,
		result: domain.ToolResult{Content: json.RawMessage(`[{"type":"text","text":"late"}]`)},
	}
	states := &irFakeStates{states: map[string]domain.ConnState{"up-a": domain.ConnAvailable}}
	sessions := &irFakeSessions{sessions: map[string]ToolCaller{"up-a": caller}}

	svc := irBuildService(cache, upstreams, aliases, mcpFilters, apiKeyFilters, states, sessions, 20*time.Millisecond)

	got, err := svc.InvokeTool(context.Background(), "", "slow_tool", json.RawMessage(`{}`))
	assertUpstreamErrorCode(t, err, domain.CodeUpstreamTimeout)
	// 不返回部分结果：超时返回零值结果。
	if got.IsError || got.Content != nil {
		t.Fatalf("超时不应返回部分结果，got=%+v", got)
	}
}

// TestInvokeRoutingSuccessPassthrough 验证端到端：成功结果原样透传，并且经别名反向映射
// 后以上游原始名与原始参数转发（Req 10.3、10.6 协同，聚焦 10.3 透传）。
func TestInvokeRoutingSuccessPassthrough(t *testing.T) {
	cache := &invFakeCache{tools: map[string][]domain.ToolDef{
		"up-a": {{OriginalName: "read_file", Name: "read_file"}},
	}}
	upstreams := &invFakeUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up-a", 0)}}
	// 别名把对外名重写为 fs_read，集成验证「对外名 → 原始名」反向映射贯通到真实转发器。
	aliases := &invFakeAliases{byUpstream: map[string][]domain.AliasRule{
		"up-a": {{Pattern: "read_file", IsRegex: false, TargetName: "fs_read", SortOrder: 0}},
	}}
	mcpFilters := &invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}}
	apiKeyFilters := &invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{}}

	want := domain.ToolResult{IsError: false, Content: json.RawMessage(`[{"type":"text","text":"ok"}]`)}
	caller := &irFakeCaller{result: want}
	states := &irFakeStates{states: map[string]domain.ConnState{"up-a": domain.ConnAvailable}}
	sessions := &irFakeSessions{sessions: map[string]ToolCaller{"up-a": caller}}

	svc := irBuildService(cache, upstreams, aliases, mcpFilters, apiKeyFilters, states, sessions, 30*time.Second)

	args := json.RawMessage(`{"path":"/tmp/a.txt"}`)
	got, err := svc.InvokeTool(context.Background(), "", "fs_read", args)
	if err != nil {
		t.Fatalf("成功调用不应返回错误，got err=%v", err)
	}
	if !caller.called.Load() {
		t.Fatalf("连接可用且工具可见时应转发到上游会话")
	}
	if caller.gotName != "read_file" {
		t.Fatalf("应按反向映射以上游原始名转发：got=%q want=%q", caller.gotName, "read_file")
	}
	if string(caller.gotArgs) != string(args) {
		t.Fatalf("原始参数未原样透传：got=%s want=%s", caller.gotArgs, args)
	}
	if got.IsError != want.IsError || string(got.Content) != string(want.Content) {
		t.Fatalf("成功结果未原样返回：got=%+v want=%+v", got, want)
	}
}

// TestInvokeRoutingUpstreamErrorPassthrough 验证端到端：上游报告的错误结果（IsError=true）
// 原样透传，且不被视为传输层错误（err 为 nil）（Req 10.3）。
func TestInvokeRoutingUpstreamErrorPassthrough(t *testing.T) {
	cache := &invFakeCache{tools: map[string][]domain.ToolDef{
		"up-a": {{OriginalName: "read_file", Name: "read_file"}},
	}}
	upstreams := &invFakeUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up-a", 0)}}
	aliases := &invFakeAliases{byUpstream: map[string][]domain.AliasRule{}}
	mcpFilters := &invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}}
	apiKeyFilters := &invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{}}

	want := domain.ToolResult{IsError: true, Content: json.RawMessage(`[{"type":"text","text":"boom"}]`)}
	caller := &irFakeCaller{result: want}
	states := &irFakeStates{states: map[string]domain.ConnState{"up-a": domain.ConnAvailable}}
	sessions := &irFakeSessions{sessions: map[string]ToolCaller{"up-a": caller}}

	svc := irBuildService(cache, upstreams, aliases, mcpFilters, apiKeyFilters, states, sessions, 30*time.Second)

	got, err := svc.InvokeTool(context.Background(), "", "read_file", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("上游错误结果应原样返回而非传输错误，got err=%v", err)
	}
	if !got.IsError || string(got.Content) != string(want.Content) {
		t.Fatalf("上游错误结果未原样返回：got=%+v want=%+v", got, want)
	}
}

// TestInvokeRoutingFilteredToolNotForwarded 验证端到端：被 API Key 级屏蔽规则过滤而不可见
// 的工具，聚合调用返回 TOOL_NOT_FOUND 且不触达真实上游转发器（连接判定亦不执行）——
// 即可见性校验先于路由（Req 10.4/11.7 与 13.7 协同，确保过滤工具不会被转发）。
func TestInvokeRoutingFilteredToolNotForwarded(t *testing.T) {
	cache := &invFakeCache{tools: map[string][]domain.ToolDef{
		"up-a": {{OriginalName: "read_file", Name: "read_file"}},
	}}
	upstreams := &invFakeUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up-a", 0)}}
	aliases := &invFakeAliases{byUpstream: map[string][]domain.AliasRule{}}
	mcpFilters := &invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}}
	// 该 API Key 启用一条屏蔽规则命中 read_file（匹配 OriginalName）。
	apiKeyFilters := &invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{
		"key-1": {{Pattern: "read_file", IsRegex: false, Enabled: true}},
	}}

	caller := &irFakeCaller{result: domain.ToolResult{Content: json.RawMessage(`[]`)}}
	// 连接登记为可用，以确认拦截发生在可见性校验阶段而非连接判定阶段。
	states := &irFakeStates{states: map[string]domain.ConnState{"up-a": domain.ConnAvailable}}
	sessions := &irFakeSessions{sessions: map[string]ToolCaller{"up-a": caller}}

	svc := irBuildService(cache, upstreams, aliases, mcpFilters, apiKeyFilters, states, sessions, 30*time.Second)

	// 无 API Key 视角下工具可见且可成功转发。
	if _, err := svc.InvokeTool(context.Background(), "", "read_file", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("无 API Key 视角下工具应可见，got err=%v", err)
	}

	// 该 API Key 视角下工具被过滤而不可见，调用应被拒且不转发。
	caller.called.Store(false)
	_, err := svc.InvokeTool(context.Background(), "key-1", "read_file", json.RawMessage(`{}`))
	assertToolNotFound(t, err)
	if caller.called.Load() {
		t.Fatalf("调用被 API Key 屏蔽的工具不应转发到上游")
	}
}
