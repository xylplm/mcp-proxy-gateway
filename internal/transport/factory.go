package transport

import (
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// factory 是 TransportFactory 的默认实现。
//
// 它实现「传输类型支持判定」「连接参数校验」以及「具体会话构造」三项职责：
// 校验通过后，按 cfg.Transport 构造对应传输类型的具体上游会话
// （stdio/SSE/Streamable-HTTP/WebSocket，任务 8.3-8.6）。
type factory struct{}

// NewFactory 构造默认传输工厂实例。
func NewFactory() TransportFactory {
	return &factory{}
}

// NewSession 先对连接参数做字段级校验（Req 4.5/4.6/4.8）：
//   - 校验失败时返回携带字段级说明的校验错误，且不构造会话、不建立连接；
//   - 校验通过后按传输类型构造对应会话对象（此时仅构造、尚未建立连接，连接由 Connect 触发）。
func (f *factory) NewSession(cfg domain.UpstreamConfig) (UpstreamSession, error) {
	// 第一步：参数校验前置，任何缺失/非法参数或不受支持的传输类型都在此拦截。
	if err := ValidateConnParams(cfg); err != nil {
		return nil, err
	}

	// 第二步：校验通过后按传输类型构造对应的具体会话（任务 8.3-8.6）。
	switch cfg.Transport {
	case domain.TransportStdio:
		return newStdioSession(cfg)
	case domain.TransportSSE:
		return newSSESession(cfg)
	case domain.TransportStreamableHTTP:
		return newStreamableHTTPSession(cfg)
	case domain.TransportWebSocket:
		return newWebSocketSession(cfg)
	case domain.TransportOpenAPI:
		return newOpenAPISession(cfg)
	default:
		// 理论上不可达：ValidateConnParams 已拒绝不受支持的类型，此处兜底保持完备。
		return nil, domain.NewValidationError(
			"上游 MCP 连接参数校验失败",
			map[string]string{"transport": "传输类型不受支持"},
		)
	}
}

// Supports 报告给定传输类型是否受支持。
//
// stdio、sse、streamable-http、websocket 四种类型返回 true，其余返回 false（Req 4.6）。
func (f *factory) Supports(t domain.TransportType) bool {
	switch t {
	case domain.TransportStdio,
		domain.TransportSSE,
		domain.TransportStreamableHTTP,
		domain.TransportWebSocket,
		domain.TransportOpenAPI:
		return true
	default:
		return false
	}
}
