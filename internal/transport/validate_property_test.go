package transport

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

// 本文件实现传输连接参数校验的属性测试（任务 8.7，Property 30）。
//
// 测试策略：以智能生成器构造「传输类型 × 连接参数」的任意组合（含受支持与不受
// 支持的传输类型，以及缺失 / 空串 / 非法 URL / 错误类型 / 合法值等各类连接参数
// 形态），再用一份不依赖被测实现的独立参考判定（connParamsValidRef）推导期望的
// 合法 / 非法结论，最后断言 ValidateConnParams 的结果与之逐一吻合：
//   - 当且仅当传输类型受支持且其全部必填参数齐备且格式合法时返回 nil；
//   - 否则返回 Code=VALIDATION 且携带字段级说明的 *domain.APIError；其中不受支持
//     的传输类型以 "transport" 字段标注，缺失 / 非法的连接参数以 "connParams.<参数名>"
//     字段标注（Req 4.5、4.6、4.8）。

// malformedURLs 是一组「确定无法被 url.Parse 解析或缺少主机」的取值，
// 用于充分锻炼 URL 非法分支（与合法 URL、错误协议、空串互补）。
var malformedURLs = []string{
	"http://[::1",   // 未闭合的 IPv6 字面量，Parse 失败
	"://nohost",     // 缺少协议
	"%zz",           // 非法百分号转义，Parse 失败
	"http://",       // 协议合法但缺少主机
	"https:///path", // 缺少主机，仅有路径
	"ws://",         // WebSocket 协议但缺少主机
	"\x7f://x",      // 含控制字符，Parse 失败
}

// genTransport 生成传输类型，覆盖受支持集合与各类不受支持取值（空串、大小写
// 变体、近似名、任意字符串），以同时锻炼「受支持」与「不受支持」两个方向。
func genTransport() *rapid.Generator[domain.TransportType] {
	return rapid.OneOf(
		rapid.SampledFrom([]domain.TransportType{
			domain.TransportStdio,
			domain.TransportSSE,
			domain.TransportStreamableHTTP,
			domain.TransportWebSocket,
		}),
		rapid.SampledFrom([]domain.TransportType{
			"", "STDIO", "Sse", "http", "grpc", "tcp", "ws", "unknown", "stdio ",
		}),
		rapid.Map(rapid.String(), func(s string) domain.TransportType {
			return domain.TransportType(s)
		}),
	)
}

// genCommandValue 生成 stdio 的 command 候选值，覆盖：缺省占位（nil）、空 / 空白
// 字符串、合法命令字符串、以及错误类型（数字 / 布尔 / 切片）。
func genCommandValue() *rapid.Generator[any] {
	return rapid.OneOf(
		rapid.Just[any](nil),
		rapid.Just[any](""),
		rapid.Just[any]("   "),
		rapid.Just[any]("\t\n"),
		rapid.Map(rapid.SampledFrom([]string{
			"node", "python3", "/usr/bin/mcp-server", "npx", "uvx",
		}), func(s string) any { return s }),
		rapid.Map(rapid.String(), func(s string) any { return s }),
		rapid.Just[any](123),
		rapid.Just[any](true),
		rapid.Just[any]([]string{"not", "a", "string"}),
	)
}

// genArgsValue 生成 stdio 的 args 候选值，覆盖：缺省占位（nil）、合法 []string、
// 合法 []any（元素全为字符串）、含非字符串元素的 []any（非法）、以及错误类型。
func genArgsValue() *rapid.Generator[any] {
	return rapid.OneOf(
		rapid.Just[any](nil),
		rapid.Map(rapid.SliceOf(rapid.String()), func(s []string) any { return s }),
		rapid.Map(rapid.SliceOf(rapid.String()), func(s []string) any {
			out := make([]any, len(s))
			for i, v := range s {
				out[i] = v
			}
			return out
		}),
		genMixedAnySlice(),
		rapid.Just[any]("notaslice"),
		rapid.Just[any](42),
		rapid.Just[any]([]int{1, 2, 3}),
	)
}

// genMixedAnySlice 生成元素类型混合的 []any（字符串与整数随机交错），
// 既可能全为字符串（合法），也可能含非字符串元素（非法），由参考判定裁决。
func genMixedAnySlice() *rapid.Generator[any] {
	return rapid.Custom(func(t *rapid.T) any {
		n := rapid.IntRange(1, 4).Draw(t, "argLen")
		out := make([]any, n)
		for i := range out {
			if rapid.Bool().Draw(t, "elemIsStr") {
				out[i] = rapid.String().Draw(t, "elemStr")
			} else {
				out[i] = rapid.IntRange(0, 100).Draw(t, "elemNum")
			}
		}
		return out
	})
}

// genHostURL 由协议 / 主机 / 路径三段拼装 URL，覆盖各类协议（含受支持与不受支持）
// 与空主机情形；当协议为空时返回不含 scheme 的相对地址。
func genHostURL() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		scheme := rapid.SampledFrom([]string{
			"http", "https", "ws", "wss", "ftp", "tcp", "",
		}).Draw(t, "scheme")
		host := rapid.SampledFrom([]string{
			"example.com", "localhost:8080", "a.b.c", "127.0.0.1", "host", "",
		}).Draw(t, "host")
		path := rapid.SampledFrom([]string{
			"", "/", "/sse", "/mcp/v1", "/messages",
		}).Draw(t, "path")
		if scheme == "" {
			return host + path
		}
		return scheme + "://" + host + path
	})
}

// genURLValue 生成 sse / streamable-http / websocket 的 url 候选值，覆盖：缺省
// 占位（nil）、空 / 空白字符串、拼装 URL（各类协议与空主机）、确定非法 URL、
// 以及错误类型。
func genURLValue() *rapid.Generator[any] {
	return rapid.OneOf(
		rapid.Just[any](nil),
		rapid.Just[any](""),
		rapid.Just[any]("   "),
		rapid.Map(genHostURL(), func(s string) any { return s }),
		rapid.Map(rapid.SampledFrom(malformedURLs), func(s string) any { return s }),
		rapid.Just[any](8080),
		rapid.Just[any](true),
	)
}

// genConnParams 组装连接参数映射：各相关键（command / url / args）的存在与否独立
// 随机，并偶尔注入无关键（不应影响校验结果），从而覆盖「缺失 / 齐备 / 多余」组合。
func genConnParams() *rapid.Generator[map[string]any] {
	return rapid.Custom(func(t *rapid.T) map[string]any {
		params := make(map[string]any)
		if rapid.Bool().Draw(t, "hasCommand") {
			params[ParamCommand] = genCommandValue().Draw(t, "commandValue")
		}
		if rapid.Bool().Draw(t, "hasURL") {
			params[ParamURL] = genURLValue().Draw(t, "urlValue")
		}
		if rapid.Bool().Draw(t, "hasArgs") {
			params[ParamArgs] = genArgsValue().Draw(t, "argsValue")
		}
		if rapid.Bool().Draw(t, "hasExtra") {
			params["extra"] = rapid.String().Draw(t, "extraValue")
		}
		return params
	})
}

// transportSupported 判定传输类型是否属于受支持集合（独立于被测实现）。
func transportSupported(tp domain.TransportType) bool {
	switch tp {
	case domain.TransportStdio,
		domain.TransportSSE,
		domain.TransportStreamableHTTP,
		domain.TransportWebSocket:
		return true
	default:
		return false
	}
}

// requireStringRef 是必填字符串参数提取的参考判定：缺失 / nil / 非字符串 / 空白
// 均视为不齐备。
func requireStringRef(params map[string]any, key string) bool {
	raw, ok := params[key]
	if !ok || raw == nil {
		return false
	}
	s, ok := raw.(string)
	if !ok {
		return false
	}
	return strings.TrimSpace(s) != ""
}

// stdioValidRef 是 stdio 连接参数的参考判定：command 必填且为非空字符串；
// 若提供 args 则须为字符串数组（[]string 或元素全为字符串的 []any）。
func stdioValidRef(params map[string]any) bool {
	if !requireStringRef(params, ParamCommand) {
		return false
	}
	if raw, ok := params[ParamArgs]; ok && raw != nil {
		switch v := raw.(type) {
		case []string:
			return true
		case []any:
			for _, item := range v {
				if _, isStr := item.(string); !isStr {
					return false
				}
			}
			return true
		default:
			return false
		}
	}
	return true
}

// urlValidRef 是 url 连接参数的参考判定：必填且为非空字符串、可被 url.Parse 解析、
// 协议位于 allowed 集合内且含主机名。使用标准库 url.Parse 复现「合法 URL」语义。
func urlValidRef(params map[string]any, allowed ...string) bool {
	raw, ok := params[ParamURL]
	if !ok || raw == nil {
		return false
	}
	s, ok := raw.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return false
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	schemeOK := false
	for _, a := range allowed {
		if u.Scheme == a {
			schemeOK = true
			break
		}
	}
	if !schemeOK {
		return false
	}
	return u.Host != ""
}

// connParamsValidRef 是连接参数整体校验的独立参考判定：按传输类型分派到对应的
// 参考判定；不受支持的传输类型一律非法。该参考不调用被测实现，作为期望对照。
func connParamsValidRef(cfg domain.UpstreamConfig) bool {
	switch cfg.Transport {
	case domain.TransportStdio:
		return stdioValidRef(cfg.ConnParams)
	case domain.TransportSSE, domain.TransportStreamableHTTP:
		return urlValidRef(cfg.ConnParams, "http", "https")
	case domain.TransportWebSocket:
		return urlValidRef(cfg.ConnParams, "ws", "wss")
	default:
		return false
	}
}

// assertConnParamsResult 断言 ValidateConnParams 的返回值与期望一致：
//   - 期望合法时必须返回 nil；
//   - 期望非法时必须返回 Code=VALIDATION 且 Fields 非空的 *domain.APIError，
//     且字段键符合标注约定（不受支持类型 → "transport"；其余 → "connParams." 前缀）。
func assertConnParamsResult(t *rapid.T, err error, wantValid, supported bool, desc string) {
	t.Helper()
	if wantValid {
		if err != nil {
			t.Fatalf("合法连接参数不应被拒绝：%s err=%v", desc, err)
		}
		return
	}
	if err == nil {
		t.Fatalf("非法连接参数必须被拒绝且不建立连接：%s", desc)
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("非法连接参数应返回 *domain.APIError：%s err=%v", desc, err)
	}
	if apiErr.Code != domain.CodeValidation {
		t.Fatalf("非法连接参数应返回 VALIDATION 类错误：%s code=%s err=%v", desc, apiErr.Code, err)
	}
	if len(apiErr.Fields) == 0 {
		t.Fatalf("校验失败应携带字段级说明：%s err=%v", desc, err)
	}
	if !supported {
		if _, has := apiErr.Fields["transport"]; !has {
			t.Fatalf("不受支持的传输类型应以 transport 字段标注：%s fields=%v", desc, apiErr.Fields)
		}
		return
	}
	for k := range apiErr.Fields {
		if !strings.HasPrefix(k, "connParams.") {
			t.Fatalf("受支持类型的参数校验错误应以 connParams. 前缀标注：%s key=%s fields=%v", desc, k, apiErr.Fields)
		}
	}
}

// Feature: mcp-proxy-gateway, Property 30: 传输连接参数校验
//
// Validates: Requirements 4.5, 4.6, 4.8
//
// 对任意「传输类型 × 连接参数」组合，ValidateConnParams 当且仅当传输类型受支持
// 且其全部必填参数齐备且格式合法时返回 nil；否则返回 Code=VALIDATION 的字段级
// 校验错误（不支持类型、缺失 / 非法参数均被拒绝，且不令该不完整配置生效）。
//
// 期望结果由不依赖被测实现的独立参考判定 connParamsValidRef 推导；非法时进一步
// 校验字段标注约定（transport / connParams.<参数名>）。rapid 默认执行 100 次迭代。
func TestProperty30TransportConnParamsValidation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cfg := domain.UpstreamConfig{
			Name:       "probe",
			Transport:  genTransport().Draw(t, "transport"),
			ConnParams: genConnParams().Draw(t, "connParams"),
		}

		wantValid := connParamsValidRef(cfg)
		supported := transportSupported(cfg.Transport)
		desc := fmt.Sprintf("{transport=%q connParams=%v}", cfg.Transport, cfg.ConnParams)

		assertConnParamsResult(t, ValidateConnParams(cfg), wantValid, supported, desc)
	})
}
