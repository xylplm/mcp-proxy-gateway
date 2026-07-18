package transport

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/runtime"
)

// 连接参数键名常量，集中定义以便各传输类型会话实现（任务 8.3-8.6）复用，
// 避免散落的魔法字符串导致键名不一致。
const (
	// ParamCommand 为 stdio 传输的可执行文件路径或命令，必填。
	ParamCommand = "command"
	// ParamArgs 为 stdio 传输的命令行参数列表，可选，需为字符串数组。
	ParamArgs = "args"
	// ParamEnv 为 stdio 传输的环境变量映射，可选，键和值均需为字符串。
	ParamEnv = "env"
	// ParamCWD 为 stdio 传输的工作目录，可选，需为非空字符串。
	ParamCWD = "cwd"
	// ParamURL 为 SSE / Streamable-HTTP / WebSocket 传输的服务地址，必填。
	ParamURL = "url"
	// ParamHeaders 为 SSE / Streamable-HTTP / WebSocket 传输的请求头映射，可选。
	ParamHeaders = "headers"
	// ParamOpenAPIBaseURL 为 OpenAPI 虚拟上游的业务 API 基础地址，必填。
	ParamOpenAPIBaseURL = "baseUrl"
	// ParamOpenAPIDocURL 为 OpenAPI 文档地址；与 docContent 二选一。
	ParamOpenAPIDocURL = "docUrl"
	// ParamOpenAPIDocContent 为 OpenAPI 文档内容；与 docUrl 二选一。
	ParamOpenAPIDocContent = "docContent"
	// ParamOpenAPIAuthType 为 OpenAPI 出站请求鉴权方式，可选。
	ParamOpenAPIAuthType = "authType"
	// ParamOpenAPIAuthName 为 API Key 或自定义 Header/Query 名称，可选。
	ParamOpenAPIAuthName = "authName"
	// ParamOpenAPIAuthValue 为 OpenAPI 鉴权值，可选，支持 ${credential}。
	ParamOpenAPIAuthValue = "authValue"
)

// 各传输类型的必填连接参数与格式约定（Req 4.5）：
//   - stdio：command（非空字符串，可执行文件路径或命令）；args/env/cwd 可选。
//   - sse：url（http:// 或 https:// 合法 URL）；headers 可选。
//   - streamable-http：url（http:// 或 https:// 合法 URL）；headers 可选。
//   - websocket：url（ws:// 或 wss:// 合法 URL）；headers 可选。
//   - openapi：baseUrl（http:// 或 https:// 合法 URL）；docUrl 或 docContent 二选一。
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
		validateOptionalStringMapParam(cfg.ConnParams, ParamHeaders, fields)
	case domain.TransportWebSocket:
		// WebSocket 要求 ws/wss 合法 URL。
		validateURLParam(cfg.ConnParams, fields, "ws", "wss")
		validateOptionalStringMapParam(cfg.ConnParams, ParamHeaders, fields)
	case domain.TransportOpenAPI:
		validateURLParamKey(cfg.ConnParams, ParamOpenAPIBaseURL, fields, "http", "https")
		validateOpenAPIDocumentParams(cfg.ConnParams, fields)
		validateOpenAPIAuthParams(cfg.ConnParams, fields)
		validateOptionalStringMapParam(cfg.ConnParams, ParamHeaders, fields)
	default:
		// 传输类型不属于受支持集合，返回指示「传输类型不受支持」的校验错误（Req 4.6）。
		fields["transport"] = fmt.Sprintf(
			"传输类型 %q 不受支持（仅支持 stdio、sse、streamable-http、websocket、openapi）",
			cfg.Transport,
		)
	}

	if len(fields) > 0 {
		return domain.NewValidationError("上游 MCP 连接参数校验失败", fields)
	}
	return nil
}

// validateStdioParams 校验 stdio 传输的连接参数：command 必填，args/env/cwd 可选。
func validateStdioParams(params map[string]any, fields map[string]string) {
	command, ok := requireStringParam(params, ParamCommand, fields)
	if ok {
		// 安全策略：危险 shell denylist + 可配置 allowlist + stdio 总开关。
		if err := runtime.ValidateCommand(command, currentPolicy()); err != nil {
			fields[fieldKey(ParamCommand)] = err.Error()
		}
	}

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

	validateOptionalStringMapParam(params, ParamEnv, fields)
	validateOptionalStringParam(params, ParamCWD, fields)
}

// validateURLParam 校验名为 url 的必填连接参数为合法 URL，且其协议位于 allowedSchemes 之内。
func validateURLParam(params map[string]any, fields map[string]string, allowedSchemes ...string) {
	validateURLParamKey(params, ParamURL, fields, allowedSchemes...)
}

func validateURLParamKey(params map[string]any, key string, fields map[string]string, allowedSchemes ...string) {
	raw, ok := requireStringParam(params, key, fields)
	if !ok {
		return
	}

	u, err := url.Parse(raw)
	if err != nil {
		fields[fieldKey(key)] = fmt.Sprintf("连接参数 %q 不是合法 URL：%v", key, err)
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
		fields[fieldKey(key)] = fmt.Sprintf(
			"连接参数 %q 协议必须为 %s",
			key,
			strings.Join(schemeSuffixes(allowedSchemes), " 或 "),
		)
		return
	}

	if u.Host == "" {
		fields[fieldKey(key)] = fmt.Sprintf("连接参数 %q 缺少主机名", key)
	}
}

func validateOpenAPIDocumentParams(params map[string]any, fields map[string]string) {
	docURL, hasDocURL := optionalOpenAPIStringParam(params, ParamOpenAPIDocURL, fields)
	docContent, _ := optionalOpenAPIStringParam(params, ParamOpenAPIDocContent, fields)
	if hasDocURL && strings.TrimSpace(docURL) != "" {
		validateURLParamKey(params, ParamOpenAPIDocURL, fields, "http", "https")
	}
	if strings.TrimSpace(docURL) == "" && strings.TrimSpace(docContent) == "" {
		fields[fieldKey(ParamOpenAPIDocURL)] = "OpenAPI 文档地址或文档内容至少填写一项"
	}
}

func validateOpenAPIAuthParams(params map[string]any, fields map[string]string) {
	authType, hasAuthType := optionalOpenAPIStringParam(params, ParamOpenAPIAuthType, fields)
	normalizedType := strings.ToLower(strings.TrimSpace(authType))
	if !hasAuthType || normalizedType == "" {
		return
	}
	switch normalizedType {
	case "none", "bearer", "basic", "api-key-header", "api-key-query", "custom-header":
	default:
		fields[fieldKey(ParamOpenAPIAuthType)] = "鉴权方式仅支持 none、bearer、basic、api-key-header、api-key-query、custom-header"
		return
	}
	if normalizedType == "api-key-header" || normalizedType == "api-key-query" || normalizedType == "custom-header" {
		name, ok := optionalOpenAPIStringParam(params, ParamOpenAPIAuthName, fields)
		if !ok || strings.TrimSpace(name) == "" {
			fields[fieldKey(ParamOpenAPIAuthName)] = "该鉴权方式需要填写 Header 或 Query 名称"
		}
	}
	optionalOpenAPIStringParam(params, ParamOpenAPIAuthValue, fields)
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
		return s, false
	}
	return s, true
}

// validateOptionalStringParam 校验可选字符串连接参数；提供时必须是非空字符串。
func validateOptionalStringParam(params map[string]any, key string, fields map[string]string) {
	optionalStringParam(params, key, fields)
}

func optionalStringParam(params map[string]any, key string, fields map[string]string) (string, bool) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return "", false
	}
	s, ok := raw.(string)
	if !ok {
		fields[fieldKey(key)] = fmt.Sprintf("连接参数 %q 必须为字符串", key)
		return "", true
	}
	if strings.TrimSpace(s) == "" {
		fields[fieldKey(key)] = fmt.Sprintf("连接参数 %q 不能为空", key)
	}
	return s, true
}

func optionalOpenAPIStringParam(params map[string]any, key string, fields map[string]string) (string, bool) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return "", false
	}
	s, ok := raw.(string)
	if !ok {
		fields[fieldKey(key)] = fmt.Sprintf("连接参数 %q 必须为字符串", key)
		return "", true
	}
	return s, true
}

// validateOptionalStringMapParam 校验可选字符串映射连接参数；提供时键和值均需为字符串。
func validateOptionalStringMapParam(params map[string]any, key string, fields map[string]string) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return
	}

	validate := func(m map[string]string) {
		for k := range m {
			if strings.TrimSpace(k) == "" {
				fields[fieldKey(key)] = fmt.Sprintf("连接参数 %q 不能包含空键", key)
				return
			}
		}
	}

	switch v := raw.(type) {
	case map[string]string:
		validate(v)
	case map[string]any:
		converted := make(map[string]string, len(v))
		for k, item := range v {
			s, ok := item.(string)
			if !ok {
				fields[fieldKey(key)] = fmt.Sprintf("连接参数 %q 的值必须为字符串", key)
				return
			}
			converted[k] = s
		}
		validate(converted)
	default:
		fields[fieldKey(key)] = fmt.Sprintf("连接参数 %q 必须为字符串映射", key)
	}
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
