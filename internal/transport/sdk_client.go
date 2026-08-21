package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/safego"
)

// 本文件为四种具体传输（stdio/SSE/Streamable-HTTP/WebSocket，任务 8.3-8.6）提供
// 共享的「MCP Go SDK 接入」适配层，统一以下与协议栈相关的通用逻辑：
//
//   - 客户端身份标识（mcp.Implementation）；
//   - 连接建立 + initialize 握手的超时控制（Req 4.9）——在已携带连接超时的 ctx 之上，
//     用「分离的连接生命周期上下文」保证握手受超时约束、而握手成功后的长连接（如 SSE/WS
//     的常驻流）不会因连接超时上下文到期而被中断；
//   - 鉴权凭证携带（Req 4.7）——HTTP 类传输（SSE/Streamable-HTTP/WebSocket）以
//     Authorization: Bearer <credential> 头携带凭证；
//   - SDK 工具类型到领域类型的映射：mcp.Tool → domain.ToolDef、mcp.CallToolResult → domain.ToolResult。
//
// 各具体传输只需构造各自的 mcp.Transport，并在 dial 回调中调用 connectWithTimeout，
// 即可复用上述全部语义。

// clientName/clientVersion 为本网关作为上游 MCP 客户端时上报的实现标识（initialize 握手中携带）。
const (
	clientName    = "mcp-proxy-gateway"
	clientVersion = "0.1.0"
)

// newMCPClient 构造一个无附加特性的 MCP 客户端，用于与单个上游建立会话。
func newMCPClient() *mcp.Client {
	return mcp.NewClient(&mcp.Implementation{Name: clientName, Version: clientVersion}, nil)
}

// connectTimeoutOf 返回当前全局连接建立超时。超时由 app 层的提供器注入，传输层
// 不直接依赖 config 包；读取时仅用于创建新会话，不会中断已有稳定连接。
func connectTimeoutOf(_ domain.UpstreamConfig) time.Duration {
	return currentConnectTimeout()
}

// connectWithTimeout 使用给定的 mcp.Transport 建立连接并完成 initialize 握手，
// 返回实现 mcpClientConn 的适配对象供 baseSession 复用（Req 4.1/4.2/4.3/4.4）。
//
// 超时与生命周期处理（Req 4.9）：传入的 ctx 已由 baseSession.establish 施加连接建立超时。
// 但 SDK 的部分传输（如 SSE 的常驻 GET 流）会把长连接绑定到传入 Connect 的上下文，
// 若直接使用 ctx，则连接成功、establish 释放 ctx 后该长连接会被一并取消。为此：
//   - 以 context.WithoutCancel(ctx) 派生一个「与超时解耦、仅保留键值」的连接生命周期上下文 connCtx，
//     交给 SDK 用于驱动整条连接；
//   - 在独立 goroutine 中执行 client.Connect，并用 select 等待「握手完成」或「ctx 超时/取消」；
//   - 若 ctx 先到期，则取消 connCtx 以中止仍在进行的握手并按超时返回（establish 会归一化为连接超时）；
//     同时兜底关闭可能在竞态下刚建立成功的会话，避免句柄/子进程泄漏。
//
// 握手成功后，connCtx 仅在 mcpClientConn.close 时被取消，从而让常驻流存活至会话关闭。
func connectWithTimeout(ctx context.Context, transport mcp.Transport) (mcpClientConn, error) {
	client := newMCPClient()
	// 记录 SDK 实际建立的底层连接，供握手失败时确定性回收（见 trackedTransport 说明）。
	tracked := &trackedTransport{inner: transport}

	// connCtx 与连接超时解耦（无截止时间、不随 ctx 取消），仅由 cancel 控制其生命周期。
	connCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	type connectResult struct {
		session *mcp.ClientSession
		err     error
	}
	resultCh := make(chan connectResult, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				safego.LogRecovered(nil, "上游 MCP SDK 连接 panic 已恢复", recovered)
				resultCh <- connectResult{
					err: domain.NewError(domain.CodeUpstreamUnavailable, "上游 MCP 连接异常"),
				}
			}
		}()
		session, err := client.Connect(connCtx, tracked, nil)
		resultCh <- connectResult{session: session, err: err}
	}()

	select {
	case res := <-resultCh:
		if res.err != nil {
			cancel()
			// 握手失败后底层连接可能仍是打开的，必须由这里关闭：stdio 传输要靠它
			// 关闭 stdin 并 Wait 子进程，否则失败重试会不断累积僵尸进程。
			tracked.closeIfOpen()
			return nil, res.err
		}
		conn := &sdkClientConn{session: res.session, cancel: cancel, done: make(chan struct{})}
		conn.watchSession()
		return conn, nil
	case <-ctx.Done():
		// 连接建立超时或被取消：中止仍在进行的握手。
		cancel()
		// 兜底：握手在竞态下可能刚好成功（关会话即可），也可能带着已打开的连接失败
		//（须关连接），两种情况都要释放 stdio 子进程等资源。
		go func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					safego.LogRecovered(nil, "上游 MCP SDK 超时清理 panic 已恢复", recovered)
				}
			}()
			res := <-resultCh
			if res.session != nil {
				_ = res.session.Close()
				return
			}
			tracked.closeIfOpen()
		}()
		return nil, ctx.Err()
	}
}

// trackedTransport 记住 Connect 产出的底层连接，使调用方在握手失败后能确定性关闭它。
//
// 为什么需要：SDK v1.6.1 的 Client.Connect 在 initialize 收发失败时会 cs.Close()，
// 但在「上游返回的协议版本不受支持」分支直接 return 而不关闭会话。对 stdio 传输，
// 这意味着子进程的 stdin 不会被关闭、cmd.Wait 也不会被调用 —— 网关是它的父进程，
// 于是每次失败都留下一个不可回收的僵尸。上游持续不可用时重试会让其无上限累积。
//
// 关闭是幂等的：SDK 的 ioConn.Close 由 closeOnce 保护，重复调用只返回首次结果，
// 因此无需判断 SDK 是否已经关过。
//
// conn 的写入发生在 Connect 内（SDK 协程），读取都在从 resultCh 收到结果之后，
// 由 channel 建立 happens-before，无需额外同步。
type trackedTransport struct {
	inner mcp.Transport
	conn  mcp.Connection
}

func (t *trackedTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	t.conn = conn
	return conn, nil
}

func (t *trackedTransport) closeIfOpen() {
	if t.conn != nil {
		_ = t.conn.Close()
	}
}

// sdkClientConn 将 MCP Go SDK 的 *mcp.ClientSession 适配为 baseSession 所需的 mcpClientConn。
//
// cancel 为连接生命周期上下文的取消函数：close 时在关闭会话后调用，确保常驻流（SSE/WS）随之释放。
type sdkClientConn struct {
	session *mcp.ClientSession
	cancel  context.CancelFunc

	// done 在 SDK 会话因远端关闭、传输终止或显式 Close 结束时关闭；waitErr 保存
	// Session.Wait 的最终原因。baseSession 将其作为可选生命周期事件暴露给 Manager。
	done      chan struct{}
	waitErr   error
	waitMu    sync.RWMutex
	closeOnce sync.Once
}

// watchSession 在独立协程中等待 SDK 会话终止。它不参与每次 RPC，因而能感知空闲期
// 的 SSE/WS 远端断连；done 只关闭一次，Close 与远端断开并发时也不会发生双关闭。
func (c *sdkClientConn) watchSession() {
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				safego.LogRecovered(nil, "上游 MCP SDK 会话等待 panic 已恢复", recovered)
				c.waitMu.Lock()
				c.waitErr = fmt.Errorf("上游 MCP 会话等待异常：%v", recovered)
				c.waitMu.Unlock()
				close(c.done)
			}
		}()
		err := c.session.Wait()
		c.waitMu.Lock()
		c.waitErr = err
		c.waitMu.Unlock()
		close(c.done)
	}()
}

// WaitClosed 等待底层 SDK 会话结束。对 Manager 而言，显式 Close 导致的取消由其
// 自身 ctx 优先处理；远端断开则返回稳定的会话失效语义。
func (c *sdkClientConn) WaitClosed(ctx context.Context) error {
	if c == nil || c.done == nil {
		return ErrSessionLost
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		c.waitMu.RLock()
		err := c.waitErr
		c.waitMu.RUnlock()
		if err == nil {
			return ErrSessionLost
		}
		return fmt.Errorf("%w: %v", ErrSessionLost, err)
	}
}

// listTools 拉取上游工具列表，按需翻页汇总，并将每个 mcp.Tool 映射为 domain.ToolDef（Req 6）。
func (c *sdkClientConn) listTools(ctx context.Context) ([]domain.ToolDef, error) {
	var (
		tools  []domain.ToolDef
		cursor string
	)
	for {
		res, err := c.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, wrapUpstreamError(err)
		}
		for _, t := range res.Tools {
			td, err := toToolDef(t)
			if err != nil {
				return nil, err
			}
			tools = append(tools, td)
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	return tools, nil
}

// callTool 转发工具调用，原始参数透传（Req 10.3），并将结果（含 IsError）映射为 domain.ToolResult。
func (c *sdkClientConn) callTool(ctx context.Context, name string, args json.RawMessage) (domain.ToolResult, error) {
	params := &mcp.CallToolParams{Name: name}
	if len(args) > 0 {
		// json.RawMessage 实现了 json.Marshaler，作为 Arguments 时按原始字节透传，不做任何改写。
		params.Arguments = args
	}

	res, err := c.session.CallTool(ctx, params)
	if err != nil {
		return domain.ToolResult{}, wrapUpstreamError(err)
	}

	content, err := marshalContent(res.Content)
	if err != nil {
		return domain.ToolResult{}, domain.NewError(
			domain.CodeUpstreamUnavailable,
			fmt.Sprintf("序列化上游 MCP 工具调用结果失败：%v", err),
		)
	}
	return domain.ToolResult{IsError: res.IsError, Content: content}, nil
}

// close 关闭底层会话并取消连接生命周期上下文。Close 会让 Session.Wait 返回，
// 由 watchSession 统一关闭 done；closeOnce 确保重复关闭不会与 SDK 会话争用。
func (c *sdkClientConn) close() error {
	var err error
	c.closeOnce.Do(func() {
		err = c.session.Close()
		if c.cancel != nil {
			c.cancel()
		}
	})
	return err
}

// toToolDef 将 SDK 工具定义映射为领域工具定义。
//
// 约定（Req 6）：OriginalName 取上游原始工具名、Name 初始等于 OriginalName（后续可被别名/去重改写）、
// Description 直接透传、InputSchema 序列化为 json.RawMessage。UpstreamID 与 Order 由上层
// （Sync_Service/聚合管线）在已知上游归属时填充，传输层不感知，故此处留空。
func toToolDef(t *mcp.Tool) (domain.ToolDef, error) {
	if t == nil {
		return domain.ToolDef{}, domain.NewError(domain.CodeUpstreamUnavailable, "上游 MCP 返回了空的工具定义")
	}

	var schema json.RawMessage
	if t.InputSchema != nil {
		raw, err := json.Marshal(t.InputSchema)
		if err != nil {
			return domain.ToolDef{}, domain.NewError(
				domain.CodeUpstreamUnavailable,
				fmt.Sprintf("序列化上游 MCP 工具 %q 的入参 Schema 失败：%v", t.Name, err),
			)
		}
		schema = raw
	}

	return domain.ToolDef{
		OriginalName: t.Name,
		Name:         t.Name,
		Description:  t.Description,
		InputSchema:  schema,
	}, nil
}

// marshalContent 将 SDK 的 content 数组序列化为原始 JSON；内容为空时返回 JSON 空数组而非 null。
func marshalContent(content []mcp.Content) (json.RawMessage, error) {
	if len(content) == 0 {
		return json.RawMessage("[]"), nil
	}
	return json.Marshal(content)
}

// IsSessionLost 判断错误是否意味着当前底层会话不可继续使用。它只识别稳定的
// 网络连接终态、会话终态和 SDK 的关闭/重连耗尽语义；普通业务错误、MCP IsError
// 结果和调用超时不会触发会话淘汰，避免错误地中断仍可使用的连接。
func IsSessionLost(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrSessionLost) || errors.Is(err, mcp.ErrSessionMissing) || errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	// 不将裸 *net.OpError 一律视为终态：Streamable HTTP 的单次请求可能临时被
	// 代理拒绝，但服务端逻辑会话和独立 SSE 流依然健康。明确的系统终态已在上方
	// 通过 errors.Is 识别；其余情况交给本次调用返回，不主动拆掉会话。
	// 当前 MCP SDK 没有导出 standalone SSE stream 的具体错误类型；此处只把 SDK
	// 明确表述的会话关闭或重连耗尽作为兼容兜底，避免把任意上游业务文本误判为断线。
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "client is closing") ||
		strings.Contains(message, "connection closed") ||
		strings.Contains(message, "failed to reconnect") ||
		strings.Contains(message, "connection reset by peer") ||
		strings.Contains(message, "broken pipe")
}

// wrapUpstreamError 将 SDK 调用过程中的错误归一化为统一错误模型，并保留根因。
// 因调用上下文超时导致的映射为上游调用超时；确认会话终态时附加 ErrSessionLost，
// 供连接管理器安全地淘汰失效 session。
func wrapUpstreamError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.WrapError(domain.CodeUpstreamTimeout, fmt.Sprintf("上游 MCP 调用超时：%v", err), err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return domain.WrapError(domain.CodeUpstreamTimeout, fmt.Sprintf("上游 MCP 调用超时：%v", err), err)
	}
	cause := err
	if IsSessionLost(err) {
		cause = fmt.Errorf("%w: %v", ErrSessionLost, err)
	}
	return domain.WrapError(domain.CodeUpstreamUnavailable, fmt.Sprintf("上游 MCP 调用失败：%v", err), cause)
}

// newAuthHTTPClient 构造一个在每个请求上附带自定义请求头与鉴权凭证的 HTTP 客户端（Req 4.7）。
//
// 当 credential 与 headers 均为空时返回 nil，使 SDK 退回到 http.DefaultClient；否则返回包装了
// authRoundTripper 的客户端。自定义 Authorization 头优先，未显式提供时再附加 Bearer 凭证。
func newAuthHTTPClient(credential string, headers map[string]string) *http.Client {
	if credential == "" && len(headers) == 0 {
		return nil
	}
	resolvedHeaders := resolveStringMapCredentials(headers, credential)
	autoBearerCredential := credential
	if stringMapContainsCredentialPlaceholder(headers) {
		autoBearerCredential = ""
	}
	return &http.Client{
		Transport: &authRoundTripper{credential: autoBearerCredential, headers: resolvedHeaders, base: http.DefaultTransport},
	}
}

// authRoundTripper 是一个为出站 HTTP 请求附加 Bearer 鉴权头的 http.RoundTripper 装饰器。
type authRoundTripper struct {
	credential string
	headers    map[string]string
	base       http.RoundTripper
}

// RoundTrip 在不修改调用方原始请求的前提下克隆请求并附加鉴权头（已存在 Authorization 时不覆盖）。
func (rt *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := rt.base
	if base == nil {
		base = http.DefaultTransport
	}
	cloned := req.Clone(req.Context())
	for k, v := range rt.headers {
		if cloned.Header.Get(k) == "" {
			cloned.Header.Set(k, v)
		}
	}
	if rt.credential != "" && cloned.Header.Get("Authorization") == "" {
		cloned.Header.Set("Authorization", "Bearer "+rt.credential)
	}
	return base.RoundTrip(cloned)
}
