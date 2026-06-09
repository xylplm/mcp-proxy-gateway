package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
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

// connectTimeoutOf 返回为给定上游应施加的连接建立超时（Req 4.9）。
//
// 连接建立超时（connect_timeout_s，默认 30s）属于全局连接配置，当前不随单个
// domain.UpstreamConfig 下发，故此处返回 0，由 newBaseSession 回退到 DefaultConnectTimeout。
// 保留该函数作为统一接入点：后续若工厂获得连接配置，仅需在此处改为读取配置值即可。
func connectTimeoutOf(_ domain.UpstreamConfig) time.Duration {
	return 0
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

	// connCtx 与连接超时解耦（无截止时间、不随 ctx 取消），仅由 cancel 控制其生命周期。
	connCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	type connectResult struct {
		session *mcp.ClientSession
		err     error
	}
	resultCh := make(chan connectResult, 1)
	go func() {
		session, err := client.Connect(connCtx, transport, nil)
		resultCh <- connectResult{session: session, err: err}
	}()

	select {
	case res := <-resultCh:
		if res.err != nil {
			cancel()
			return nil, res.err
		}
		return &sdkClientConn{session: res.session, cancel: cancel}, nil
	case <-ctx.Done():
		// 连接建立超时或被取消：中止仍在进行的握手。
		cancel()
		// 兜底：若握手在竞态下刚好成功，关闭其会话以释放资源（如 stdio 子进程）。
		go func() {
			if res := <-resultCh; res.session != nil {
				_ = res.session.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

// sdkClientConn 将 MCP Go SDK 的 *mcp.ClientSession 适配为 baseSession 所需的 mcpClientConn。
//
// cancel 为连接生命周期上下文的取消函数：close 时在关闭会话后调用，确保常驻流（SSE/WS）随之释放。
type sdkClientConn struct {
	session *mcp.ClientSession
	cancel  context.CancelFunc
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

// close 关闭底层会话并取消连接生命周期上下文。重复调用安全（由 SDK 会话与 cancel 自身保证幂等）。
func (c *sdkClientConn) close() error {
	err := c.session.Close()
	if c.cancel != nil {
		c.cancel()
	}
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

// wrapUpstreamError 将 SDK 调用过程中的错误归一化为统一错误模型：
// 因上下文超时导致的映射为上游调用超时，其余映射为上游不可用。
func wrapUpstreamError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.NewError(domain.CodeUpstreamTimeout, fmt.Sprintf("上游 MCP 调用超时：%v", err))
	}
	return domain.NewError(domain.CodeUpstreamUnavailable, fmt.Sprintf("上游 MCP 调用失败：%v", err))
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
