package aggregation

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件为任务 11.1「将 InvokeTool 接入真实上游会话与超时控制」的单元测试，验证
// SessionInvoker 的核心调用语义（Req 10.3、10.5、10.8）：
//   - 连接不可用：状态非 available 时拒绝且不发起任何上游调用，返回 UPSTREAM_UNAVAILABLE；
//   - 调用超时：上游在超时时长内未返回时中止、不返回部分结果，返回 UPSTREAM_TIMEOUT；
//   - 成功透传：成功结果原样返回；
//   - 上游错误透传：上游报告的错误结果（IsError=true）原样返回。
//
// 文件内的内存假实现统一使用 si 前缀命名，避免与同包内其它测试标识符冲突。

// siFakeStates 是 ConnStateProvider 的内存假实现，按上游标识返回预置状态与失败原因。
type siFakeStates struct {
	state   domain.ConnState
	lastErr string
}

func (s *siFakeStates) GetState(_ string) (domain.ConnState, string) {
	return s.state, s.lastErr
}

// siFakeSession 是 ToolCaller 的内存假实现，记录是否被调用并按预置行为返回。
//
// 当 delay > 0 时模拟一个耗时调用：在 delay 内若 ctx 被取消（如超时），返回
// ctx.Err()，用于驱动超时分支；否则返回预置的 result/err。
type siFakeSession struct {
	called  atomic.Bool
	gotName string
	gotArgs json.RawMessage
	delay   time.Duration
	result  domain.ToolResult
	err     error
}

func (s *siFakeSession) CallTool(ctx context.Context, name string, args json.RawMessage) (domain.ToolResult, error) {
	s.called.Store(true)
	s.gotName = name
	s.gotArgs = args
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return domain.ToolResult{}, ctx.Err()
		}
	}
	return s.result, s.err
}

// siFakeSessions 是 SessionProvider 的内存假实现。ok 为 false 时模拟无可用会话。
type siFakeSessions struct {
	session ToolCaller
	ok      bool
}

func (s *siFakeSessions) Session(_ string) (ToolCaller, bool) {
	return s.session, s.ok
}

type siEchoSession struct {
	calls atomic.Int64
}

func (s *siEchoSession) CallTool(ctx context.Context, _ string, args json.RawMessage) (domain.ToolResult, error) {
	select {
	case <-time.After(time.Duration(len(args)%5) * time.Millisecond):
	case <-ctx.Done():
		return domain.ToolResult{}, ctx.Err()
	}
	s.calls.Add(1)
	content := append(json.RawMessage(nil), args...)
	return domain.ToolResult{Content: content}, nil
}

type siPanicSession struct{}

func (s siPanicSession) CallTool(context.Context, string, json.RawMessage) (domain.ToolResult, error) {
	panic("sdk call panic")
}

// assertUpstreamErrorCode 断言 err 是指定错误码的 *domain.APIError。
func assertUpstreamErrorCode(t *testing.T, err error, want domain.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望返回 %s 错误，但 err 为 nil", want)
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望 *domain.APIError，got %T: %v", err, err)
	}
	if apiErr.Code != want {
		t.Fatalf("期望错误码 %s，got %s", want, apiErr.Code)
	}
}

// TestSessionInvokerUnavailableRejected 验证：连接状态非 available 时拒绝转发，
// 返回 UPSTREAM_UNAVAILABLE 且不调用会话（Req 10.5）。
func TestSessionInvokerUnavailableRejected(t *testing.T) {
	for _, state := range []domain.ConnState{
		domain.ConnConnecting,
		domain.ConnUnavailable,
		domain.ConnSuspended,
	} {
		t.Run(string(state), func(t *testing.T) {
			session := &siFakeSession{result: domain.ToolResult{Content: json.RawMessage(`[]`)}}
			invoker := NewSessionInvoker(
				&siFakeStates{state: state, lastErr: "拨号失败"},
				&siFakeSessions{session: session, ok: true},
				30*time.Second,
				nil,
			)

			_, err := invoker.CallUpstream(context.Background(), "up-a", "read_file", json.RawMessage(`{}`))
			assertUpstreamErrorCode(t, err, domain.CodeUpstreamUnavailable)
			if session.called.Load() {
				t.Fatalf("连接不可用时不应向上游会话发起调用")
			}
		})
	}
}

// TestSessionInvokerNoSessionRejected 验证：连接可用但无可用会话时，按不可用处理且
// 不发起调用（Req 10.5）。
func TestSessionInvokerNoSessionRejected(t *testing.T) {
	invoker := NewSessionInvoker(
		&siFakeStates{state: domain.ConnAvailable},
		&siFakeSessions{session: nil, ok: false},
		30*time.Second,
		nil,
	)

	_, err := invoker.CallUpstream(context.Background(), "up-a", "read_file", json.RawMessage(`{}`))
	assertUpstreamErrorCode(t, err, domain.CodeUpstreamUnavailable)
}

// TestSessionInvokerSuccessPassthrough 验证：连接可用且会话成功返回时，结果原样透传，
// 并以原始名与原始参数转发（Req 10.3）。
func TestSessionInvokerSuccessPassthrough(t *testing.T) {
	want := domain.ToolResult{IsError: false, Content: json.RawMessage(`[{"type":"text","text":"ok"}]`)}
	session := &siFakeSession{result: want}
	invoker := NewSessionInvoker(
		&siFakeStates{state: domain.ConnAvailable},
		&siFakeSessions{session: session, ok: true},
		30*time.Second,
		nil,
	)

	args := json.RawMessage(`{"path":"/tmp/a.txt"}`)
	got, err := invoker.CallUpstream(context.Background(), "up-a", "read_file", args)
	if err != nil {
		t.Fatalf("成功调用不应返回错误，got err=%v", err)
	}
	if !session.called.Load() {
		t.Fatalf("连接可用时应转发到上游会话")
	}
	if session.gotName != "read_file" {
		t.Fatalf("转发上游原始名错误：got=%q want=%q", session.gotName, "read_file")
	}
	if string(session.gotArgs) != string(args) {
		t.Fatalf("原始参数未原样透传：got=%s want=%s", session.gotArgs, args)
	}
	if got.IsError != want.IsError || string(got.Content) != string(want.Content) {
		t.Fatalf("成功结果未原样返回：got=%+v want=%+v", got, want)
	}
}

// TestSessionInvokerUpstreamErrorPassthrough 验证：上游报告的错误结果（IsError=true）
// 原样返回，且不被视为传输层错误（err 为 nil）（Req 10.3）。
func TestSessionInvokerUpstreamErrorPassthrough(t *testing.T) {
	want := domain.ToolResult{IsError: true, Content: json.RawMessage(`[{"type":"text","text":"boom"}]`)}
	session := &siFakeSession{result: want}
	invoker := NewSessionInvoker(
		&siFakeStates{state: domain.ConnAvailable},
		&siFakeSessions{session: session, ok: true},
		30*time.Second,
		nil,
	)

	got, err := invoker.CallUpstream(context.Background(), "up-a", "read_file", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("上游错误结果应原样返回而非传输错误，got err=%v", err)
	}
	if !got.IsError || string(got.Content) != string(want.Content) {
		t.Fatalf("上游错误结果未原样返回：got=%+v want=%+v", got, want)
	}
}

// TestSessionInvokerTimeoutAborts 验证：上游在超时时长内未返回时中止本次调用、
// 不返回部分结果并返回 UPSTREAM_TIMEOUT（Req 10.8）。
func TestSessionInvokerTimeoutAborts(t *testing.T) {
	// 会话耗时远超超时时长，触发超时分支。
	session := &siFakeSession{
		delay:  500 * time.Millisecond,
		result: domain.ToolResult{Content: json.RawMessage(`[{"type":"text","text":"late"}]`)},
	}
	invoker := NewSessionInvoker(
		&siFakeStates{state: domain.ConnAvailable},
		&siFakeSessions{session: session, ok: true},
		20*time.Millisecond,
		nil,
	)

	got, err := invoker.CallUpstream(context.Background(), "up-a", "read_file", json.RawMessage(`{}`))
	assertUpstreamErrorCode(t, err, domain.CodeUpstreamTimeout)
	// 不返回部分结果：超时返回零值结果。
	if got.IsError || got.Content != nil {
		t.Fatalf("超时不应返回部分结果，got=%+v", got)
	}
}

// TestSessionInvokerDefaultTimeout 验证：构造时传入非正超时回退到默认值，
// 不影响正常成功调用。
func TestSessionInvokerDefaultTimeout(t *testing.T) {
	session := &siFakeSession{result: domain.ToolResult{Content: json.RawMessage(`[]`)}}
	invoker := NewSessionInvoker(
		&siFakeStates{state: domain.ConnAvailable},
		&siFakeSessions{session: session, ok: true},
		0, // 非正值回退到 DefaultUpstreamCallTimeout
		nil,
	)
	if invoker.callTimeout != DefaultUpstreamCallTimeout {
		t.Fatalf("非正超时应回退到默认值：got=%v want=%v", invoker.callTimeout, DefaultUpstreamCallTimeout)
	}
	if _, err := invoker.CallUpstream(context.Background(), "up-a", "read_file", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("默认超时下成功调用不应返回错误，got err=%v", err)
	}
}

func TestSessionInvokerRecoversSessionPanic(t *testing.T) {
	invoker := NewSessionInvoker(
		&siFakeStates{state: domain.ConnAvailable},
		&siFakeSessions{session: siPanicSession{}, ok: true},
		30*time.Second,
		nil,
	)

	got, err := invoker.CallUpstream(context.Background(), "up-a", "panic_tool", json.RawMessage(`{}`))
	assertUpstreamErrorCode(t, err, domain.CodeInternal)
	if got.IsError || got.Content != nil {
		t.Fatalf("panic 兜底不应返回部分结果：got=%+v", got)
	}
}
