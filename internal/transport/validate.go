package transport

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 连接参数键名常量，集中定义以便各传输类型会话实现（任务 8.3-8.6）复用，
// 避免散落的魔法字符串导致键名不一致。
const (
	// ParamCommand 为 stdio 传输的可执行文件路径或命令，必填。
	ParamCommand = "command"
	// ParamArgs 为 stdio 传输的命令行参数列表，可选，需为字符串数组。
	ParamArgs = "args"
	// ParamURL 为 SSE / Streamable-HTTP / WebSocket 传输的服务地址，必填。
	ParamURL = "url"
)

// 各传输类型的必填连接参数与格式约定（Req 4.5）：
//   - stdio：command（非空字符串，可执行文件路径或命令）；args 可选，字符串数组。
//   - sse：url（http:// 或 https:// 合法 URL）。
//   - streamable-http：url（http:// 或 https:// 合法 URL）。
//   - websocket：url（ws:// 或 wss:// 合法 URL）。
//
// 不在上述集合内的传输类型视为不受支持（Req 4.6）。

// ValidateConnParams 按上游配置所声明的传输类型校验其连接参数是否齐备且格式合法。
//
// 该函数为独立导出的纯函数，便于属性测试（任务 8.7，Property 30）直接覆盖：
// 当且仅当传输类型受支持且其全部必填参数齐备且格式合法时返回 nil；否则返回
// 携带字段级说明的校验类 APIError（Code=VALIDATION，Fields 标注具体的缺失/非法
// 参数），调用方据此拒绝建立连接、不令该不完整配置生效（Req 4.6、4.8）。
func ValidateConnParams(cfg domain.UpstreamConfig) error {
	fields := make(map[string]string)

	switch cfg.Transport {
	case domain.TransportStdio:
		validateStdioParams(cfg.ConnParams, fields)
	case domain.TransportSSE, domain.TransportStreamableHTTP:
		// SSE 与 Streamable-HTTP 均要求 http/https 合法 URL。
		validateURLParam(cfg.ConnParams, fields, "http", "https")
	case domain.TransportWebSocket:
		// WebSocket 要求 ws/wss 合法 URL。
		validateURLParam(cfg.ConnParams, fields, "ws", "wss")
	default:
		// 传输类型不属于受支持集合，返回指示「传输类型不受支持」的校验错误（Req 4.6）。
		fields["transport"] = fmt.Sprintf(
			"传输类型 %q 不受支持（仅支持 stdio、sse、streamable-http、websocket）",
			cfg.Transport,
		)
	}

	if len(fields) > 0 {
		return domain.NewValidationError("上游 MCP 连接参数校验失败", fields)
	}
	return nil
}

// validateStdioParams 校验 stdio 传输的连接参数：command 必填且为非空字符串，args 可选且为字符串数组。
func validateStdioParams(params map[string]any, fields map[string]string) {
	requireStringParam(params, ParamCommand, fields)

	// args 为可选参数；若提供则必须为字符串数组（兼容 JSON 解析得到的 []any）。
	if raw, ok := params[ParamArgs]; ok && raw != nil {
		switch v := raw.(type) {
		case []string:
			// 已是字符串数组，合法。
		case []any:
			for i, item := range v {
				if _, isStr := item.(string); !isStr {
					fields[fieldKey(ParamArgs)] = fmt.Sprintf("连接参数 %q 第 %d 个元素必须为字符串", ParamArgs, i)
					return
				}
			}
		default:
			fields[fieldKey(ParamArgs)] = fmt.Sprintf("连接参数 %q 必须为字符串数组", ParamArgs)
		}
	}
}

// validateURLParam 校验名为 url 的必填连接参数为合法 URL，且其协议位于 allowedSchemes 之内。
func validateURLParam(params map[string]any, fields map[string]string, allowedSchemes ...string) {
	raw, ok := requireStringParam(params, ParamURL, fields)
	if !ok {
		return
	}

	u, err := url.Parse(raw)
	if err != nil {
		fields[fieldKey(ParamURL)] = fmt.Sprintf("连接参数 %q 不是合法 URL：%v", ParamURL, err)
		return
	}

	schemeOK := false
	for _, s := range allowedSchemes {
		if u.Scheme == s {
			schemeOK = true
			break
		}
	}
	if !schemeOK {
		fields[fieldKey(ParamURL)] = fmt.Sprintf(
			"连接参数 %q 协议必须为 %s",
			ParamURL,
			strings.Join(schemeSuffixes(allowedSchemes), " 或 "),
		)
		return
	}

	if u.Host == "" {
		fields[fieldKey(ParamURL)] = fmt.Sprintf("连接参数 %q 缺少主机名", ParamURL)
	}
}

// requireStringParam 提取必填的字符串连接参数；缺失、类型不符或空白时向 fields 写入字段级错误并返回 false。
func requireStringParam(params map[string]any, key string, fields map[string]string) (string, bool) {
	raw, ok := params[key]
	if !ok || raw == nil {
		fields[fieldKey(key)] = fmt.Sprintf("缺少必填连接参数 %q", key)
		return "", false
	}
	s, ok := raw.(string)
	if !ok {
		fields[fieldKey(key)] = fmt.Sprintf("连接参数 %q 必须为字符串", key)
		return "", false
	}
	if strings.TrimSpace(s) == "" {
		fields[fieldKey(key)] = fmt.Sprintf("连接参数 %q 不能为空", key)
		return "", false
	}
	return s, true
}

// fieldKey 将连接参数键名转换为字段级错误中使用的限定路径，便于前端定位到具体字段。
func fieldKey(param string) string {
	return "connParams." + param
}

// schemeSuffixes 将协议名渲染为带 :// 后缀的展示形式，用于错误提示。
func schemeSuffixes(schemes []string) []string {
	out := make([]string, len(schemes))
	for i, s := range schemes {
		out[i] = s + "://"
	}
	return out
}
