package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件（任务 8.2）实现「传输无关」的统一会话基础设施，为后续四种具体传输
// （stdio/SSE/Streamable-HTTP/WebSocket，任务 8.3-8.6）提供共享的生命周期语义与脚手架：
//
//   - 连接建立超时约束：initialize 握手受 connect_timeout_s 约束（默认 30s，Req 4.9）；
//   - 鉴权凭证携带：会话持有所配置的鉴权凭证明文，供各传输在建立连接时附带（Req 4.7）；
//   - 连接参数解析：从 domain.UpstreamConfig.ConnParams 提取 command/args/url，复用 validate.go 的键名常量；
//   - 会话状态语义：统一管理 idle→connecting→connected→closed 状态机，并在未连接时拒绝 ListTools/CallTool。
//
// 设计要点：baseSession 已完整实现 ListTools/CallTool/Close 三个方法（委托给底层
// mcpClientConn），各具体传输只需实现各自的 Connect——在其中构造传输特定的底层连接
// （通常封装 MCP Go SDK 的 client session 与对应 transport），并通过 establish 注入。
// 由此 8.3-8.6 仅需提供一个「dial 回调」即可复用全部超时/状态/守卫逻辑。
//
// 关于 MCP Go SDK：本任务刻意不引入 SDK 依赖。会话状态、超时与凭证语义均与具体协议栈
// 无关，而 SDK client/transport 的构造是 8.3-8.6 的职责。各传输接入 SDK 的位置即为
// 传入 establish 的 dial 回调（见下方 mcpClientConn 接口与 establish 方法的 TODO 标注）。

// DefaultConnectTimeout 为连接建立（含 MCP initialize 握手）的默认超时时长（Req 4.9）。
// 当上游未显式配置 connect_timeout_s 或配置为非正值时回退到该默认值。
const DefaultConnectTimeout = 30 * time.Second

// 会话生命周期共享错误。均为统一错误模型（domain.APIError），便于上层按错误码分类处理。
var (
	// ErrSessionNotConnected 表示在会话尚未成功建立连接时调用了 ListTools/CallTool。
	ErrSessionNotConnected = domain.NewError(domain.CodeUpstreamUnavailable, "上游 MCP 会话尚未建立连接")
	// ErrSessionClosed 表示会话已关闭，不能再建立连接或收发消息。
	ErrSessionClosed = domain.NewError(domain.CodeUpstreamUnavailable, "上游 MCP 会话已关闭")
	// ErrSessionBusy 表示会话已处于已连接或正在连接状态，不能重复发起连接。
	ErrSessionBusy = domain.NewError(domain.CodeUpstreamUnavailable, "上游 MCP 会话已建立或正在建立连接")
	// ErrConnectTimeout 表示连接建立在配置的超时时长内未完成 MCP 初始化握手（Req 4.9）。
	ErrConnectTimeout = domain.NewError(domain.CodeUpstreamTimeout, "上游 MCP 连接建立超时")
)

// sessionState 表示单条会话的内部生命周期状态。
type sessionState int32

const (
	// stateIdle 表示会话已构造但尚未建立连接。
	stateIdle sessionState = iota
	// stateConnecting 表示正在建立连接并完成 MCP initialize 握手。
	stateConnecting
	// stateConnected 表示连接已建立、握手完成，可正常收发消息。
	stateConnected
	// stateClosed 表示会话已关闭，进入终态。
	stateClosed
)

// connParams 为从 domain.UpstreamConfig.ConnParams 解析出的、与具体传输相关的连接参数。
// 字段在不同传输类型下取舍不同：stdio 使用 command/args；sse/streamable-http/websocket 使用 url。
type connParams struct {
	// command 为 stdio 传输启动上游 MCP 子进程的可执行文件路径或命令。
	command string
	// args 为 stdio 传输的命令行参数列表。
	args []string
	// url 为 sse/streamable-http/websocket 传输的服务地址。
	url string
}

// parseConnParams 在校验通过的前提下从配置中提取连接参数。
//
// 为保证健壮性，它先复用 ValidateConnParams 做字段级校验（Req 4.5/4.6/4.8），
// 校验失败时直接返回该校验错误；校验通过后再按传输类型安全地提取 command/args/url，
// 复用 validate.go 中的键名常量（ParamCommand/ParamArgs/ParamURL）避免魔法字符串散落。
func parseConnParams(cfg domain.UpstreamConfig) (connParams, error) {
	if err := ValidateConnParams(cfg); err != nil {
		return connParams{}, err
	}

	var p connParams
	switch cfg.Transport {
	case domain.TransportStdio:
		// command 经校验必为非空字符串。
		if s, ok := cfg.ConnParams[ParamCommand].(string); ok {
			p.command = s
		}
		// args 为可选参数，可能是 []string 或 JSON 解析得到的 []any。
		p.args = toStringSlice(cfg.ConnParams[ParamArgs])
	case domain.TransportSSE, domain.TransportStreamableHTTP, domain.TransportWebSocket:
		// url 经校验必为合法且非空的字符串。
		if s, ok := cfg.ConnParams[ParamURL].(string); ok {
			p.url = s
		}
	}
	return p, nil
}

// toStringSlice 将连接参数中的 args 归一化为 []string，兼容 []string 与 []any 两种来源。
// 非数组或包含非字符串元素时返回 nil（此类非法输入已在 ValidateConnParams 阶段被拦截）。
func toStringSlice(raw any) []string {
	switch v := raw.(type) {
	case nil:
		return nil
	case []string:
		out := make([]string, len(v))
		copy(out, v)
		return out
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil
			}
			out = append(out, s)
		}
		return out
	default:
		return nil
	}
}

// mcpClientConn 抽象一条已建立的上游 MCP 客户端连接，屏蔽具体 SDK 与传输细节。
//
// 接入点（任务 8.3-8.6）：各具体传输会话负责构造实现本接口的对象——通常封装 MCP Go SDK
// 的 client session 与对应 transport（stdio 子进程 / SSE / Streamable-HTTP / WebSocket），
// 并在其 Connect 中通过 baseSession.establish 注入。baseSession 据此对外统一提供
// 状态守卫、超时控制与消息收发委托，无需各传输各自重复实现这些通用语义。
type mcpClientConn interface {
	// listTools 拉取上游工具列表（供 Sync_Service 使用，Req 6）。
	listTools(ctx context.Context) ([]domain.ToolDef, error)
	// callTool 转发工具调用，原始参数透传（Req 10.3）。
	callTool(ctx context.Context, name string, args json.RawMessage) (domain.ToolResult, error)
	// close 关闭底层连接并释放资源。
	close() error
}

// baseSession 是各具体传输会话共享的基础实现，封装连接参数、凭证、超时与状态机。
//
// 它已实现 UpstreamSession 的 ListTools/CallTool/Close 三个方法（委托给底层 mcpClientConn），
// 但刻意不实现 Connect——Connect 由各具体传输（8.3-8.6）实现，并在其中调用 establish 注入
// 传输特定的底层连接。因此 baseSession 自身不满足 UpstreamSession，需被具体会话内嵌补齐 Connect。
type baseSession struct {
	// cfg 为该会话对应的上游配置（含传输类型、凭证等）。
	cfg domain.UpstreamConfig
	// params 为解析后的连接参数（command/args/url）。
	params connParams
	// credential 为该上游配置的鉴权凭证明文，供各传输在建立连接时携带（Req 4.7）。
	credential string
	// connectTimeout 为连接建立（含握手）的超时时长（Req 4.9）。
	connectTimeout time.Duration

	mu    sync.Mutex
	state sessionState
	// conn 为连接成功后由 establish 写入的底层连接；未连接时为 nil。
	conn mcpClientConn
}

// newBaseSession 解析连接参数并构造共享会话基础结构。
//
// connectTimeout 为非正值时回退到 DefaultConnectTimeout（默认 30s，Req 4.9）。
// 连接参数校验失败时返回字段级校验错误且不构造会话（与 factory.NewSession 的契约一致）。
func newBaseSession(cfg domain.UpstreamConfig, connectTimeout time.Duration) (*baseSession, error) {
	params, err := parseConnParams(cfg)
	if err != nil {
		return nil, err
	}
	if connectTimeout <= 0 {
		connectTimeout = DefaultConnectTimeout
	}
	return &baseSession{
		cfg:            cfg,
		params:         params,
		credential:     cfg.Credential,
		connectTimeout: connectTimeout,
		state:          stateIdle,
	}, nil
}

// connectContext 基于父上下文派生一个受连接建立超时约束的子上下文（Req 4.9）。
// 调用方必须在使用完毕后调用返回的 cancel 以释放资源。
func (s *baseSession) connectContext(parent context.Context) (context.Context, context.CancelFunc) {
	timeout := s.connectTimeout
	if timeout <= 0 {
		timeout = DefaultConnectTimeout
	}
	return context.WithTimeout(parent, timeout)
}

// establish 是各具体传输 Connect 的共享脚手架：
//
//  1. 校验当前状态可发起连接（仅允许从 idle 进入 connecting）；
//  2. 派生受连接超时约束的上下文（Req 4.9）；
//  3. 调用由具体传输提供的 dial 回调建立底层连接并完成 initialize 握手；
//  4. 成功则置为已连接并保存连接；失败则回退状态并对超时进行归一化分类（Req 4.9）。
//
// 接入点（任务 8.3-8.6）：dial 回调即各传输接入 MCP Go SDK 的位置——在其中构造 SDK
// client 与对应 transport、附带鉴权凭证（Req 4.7）、执行 Connect/Initialize，并返回
// 一个 mcpClientConn 适配对象。dial 收到的 ctx 已带连接超时，握手应遵循其取消信号。
func (s *baseSession) establish(parent context.Context, dial func(ctx context.Context) (mcpClientConn, error)) error {
	if dial == nil {
		// 编程错误：具体传输未提供连接建立回调。
		return domain.NewError(domain.CodeValidation, "传输会话未提供连接建立回调")
	}
	if err := s.beginConnect(); err != nil {
		return err
	}

	ctx, cancel := s.connectContext(parent)
	defer cancel()

	conn, err := dial(ctx)
	if err != nil {
		s.abortConnect()
		return classifyConnectError(ctx, err)
	}

	s.completeConnect(conn)
	return nil
}

// beginConnect 将状态从 idle 切换为 connecting；其余状态分别返回对应的语义错误。
func (s *baseSession) beginConnect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state {
	case stateClosed:
		return ErrSessionClosed
	case stateConnecting, stateConnected:
		return ErrSessionBusy
	default:
		s.state = stateConnecting
		return nil
	}
}

// abortConnect 在连接建立失败后将状态回退；若期间会话已被关闭则保持关闭终态。
func (s *baseSession) abortConnect() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == stateConnecting {
		s.state = stateIdle
	}
}

// completeConnect 在连接建立成功后保存底层连接并置为已连接；
// 若期间会话已被并发关闭，则立即关闭刚建立的连接以避免资源泄漏。
func (s *baseSession) completeConnect(conn mcpClientConn) {
	s.mu.Lock()
	if s.state != stateConnecting {
		// 期间已被 Close：放弃本次连接并在锁外关闭，避免句柄泄漏。
		s.mu.Unlock()
		if conn != nil {
			_ = conn.close()
		}
		return
	}
	s.conn = conn
	s.state = stateConnected
	s.mu.Unlock()
}

// connected 返回当前已连接的底层连接；未处于已连接状态时返回相应错误（Req：未连接拒绝收发）。
func (s *baseSession) connected() (mcpClientConn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state {
	case stateConnected:
		if s.conn == nil {
			return nil, ErrSessionNotConnected
		}
		return s.conn, nil
	case stateClosed:
		return nil, ErrSessionClosed
	default:
		return nil, ErrSessionNotConnected
	}
}

// ListTools 在会话已连接时委托底层连接拉取工具列表；未连接时返回 ErrSessionNotConnected。
func (s *baseSession) ListTools(ctx context.Context) ([]domain.ToolDef, error) {
	conn, err := s.connected()
	if err != nil {
		return nil, err
	}
	return conn.listTools(ctx)
}

// CallTool 在会话已连接时委托底层连接转发工具调用（原始参数透传，Req 10.3）；
// 未连接时返回 ErrSessionNotConnected。
func (s *baseSession) CallTool(ctx context.Context, name string, args json.RawMessage) (domain.ToolResult, error) {
	conn, err := s.connected()
	if err != nil {
		return domain.ToolResult{}, err
	}
	return conn.callTool(ctx, name, args)
}

// Close 关闭会话：置为终态并关闭底层连接（若已建立）。重复调用安全（幂等）。
func (s *baseSession) Close() error {
	s.mu.Lock()
	if s.state == stateClosed {
		s.mu.Unlock()
		return nil
	}
	conn := s.conn
	s.conn = nil
	s.state = stateClosed
	s.mu.Unlock()

	if conn != nil {
		return conn.close()
	}
	return nil
}

// classifyConnectError 将 dial 回调返回的错误归一化：
// 若因连接超时上下文到期导致（context.DeadlineExceeded），统一映射为 ErrConnectTimeout（Req 4.9）；
// 上下文被取消映射为不可用；其余错误包装为「连接建立失败」的上游不可用错误，保留原始原因。
func classifyConnectError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrConnectTimeout
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return domain.NewError(domain.CodeUpstreamUnavailable, "上游 MCP 连接建立被取消")
	}
	return domain.NewError(domain.CodeUpstreamUnavailable, fmt.Sprintf("上游 MCP 连接建立失败：%v", err))
}
