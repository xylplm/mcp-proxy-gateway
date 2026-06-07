package xiaozhi

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

// 本文件实现设计文档「Correctness Properties」中的 Property 29（小智接入点地址协议
// 校验），对应 tasks.md 任务 21.3，验证 Req 15.6。
//
// 被测对象为 xiaozhi.ValidateEndpoint(endpoint) 纯函数，其文档化语义为：当且仅当地址
// 为以 ws:// 或 wss:// 为协议前缀的合法 WebSocket URL（含非空主机名）时校验通过返回
// nil；否则返回 domain.CodeValidation 类 APIError（字段 xiaozhi.endpoint）。
//
// 测试以「按构造分类生成」覆盖 iff 关系：分别生成必然合法（accept）与必然非法（reject）
// 两类地址，避免在测试中重复实现被测判定逻辑。所有辅助标识以 p29 前缀命名，避免与同包
// backoff_validate_test.go 等文件冲突。

// p29GenHostname 生成一个合法且非空的主机名（不含端口）：1-3 段 [a-z0-9] 标签以 "." 连接。
// 此字符集保证 url.Parse 不报错且 Host 非空，可安全用于构造「必然合法」地址。
func p29GenHostname(t *rapid.T) string {
	label := rapid.StringOfN(rapid.RuneFrom([]rune("abcdefghijklmnopqrstuvwxyz0123456789")), 1, 10, -1)
	n := rapid.IntRange(1, 3).Draw(t, "hostLabels")
	labels := make([]string, n)
	for i := range labels {
		labels[i] = label.Draw(t, fmt.Sprintf("label%d", i))
	}
	return strings.Join(labels, ".")
}

// p29GenValidEndpoint 构造一个必然被接受的地址：协议取 ws/wss（含大小写变体，url.Parse
// 会将 scheme 归一化为小写，故大写前缀同样合法），主机名非空，可选端口、路径与查询串，
// 字符集受限以确保 url.Parse 解析成功。
func p29GenValidEndpoint(t *rapid.T) string {
	scheme := rapid.SampledFrom([]string{"ws", "wss", "WS", "WSS", "Ws", "wSs"}).Draw(t, "validScheme")
	host := p29GenHostname(t)
	if rapid.Bool().Draw(t, "withPort") {
		host += ":" + fmt.Sprintf("%d", rapid.IntRange(1, 65535).Draw(t, "port"))
	}
	path := ""
	if rapid.Bool().Draw(t, "withPath") {
		seg := rapid.StringOfN(rapid.RuneFrom([]rune("abcdefghijklmnopqrstuvwxyz0123456789/")), 1, 20, -1).Draw(t, "path")
		path = "/" + strings.TrimPrefix(seg, "/")
	}
	query := ""
	if rapid.Bool().Draw(t, "withQuery") {
		key := rapid.StringOfN(rapid.RuneFrom([]rune("abcdefghijklmnopqrstuvwxyz")), 1, 8, -1).Draw(t, "qkey")
		val := rapid.StringOfN(rapid.RuneFrom([]rune("abcdefghijklmnopqrstuvwxyz0123456789")), 1, 8, -1).Draw(t, "qval")
		query = "?" + key + "=" + val
	}
	return scheme + "://" + host + path + query
}

// p29GenInvalidEndpoint 构造一个必然被拒绝的地址，覆盖多类非法形态：
//   - 空 / 仅空白；
//   - 错误协议前缀（http/https/ftp/tcp/... 等非 ws/wss）；
//   - 缺少协议前缀（裸主机名 / "//host"）；
//   - 缺少主机名（"ws://"、"ws:///path"）；
//   - 非法 URL（含空格等使 url.Parse 失败的字符）。
func p29GenInvalidEndpoint(t *rapid.T) string {
	host := p29GenHostname(t)
	kind := rapid.IntRange(0, 4).Draw(t, "invalidKind")
	switch kind {
	case 0: // 空 / 仅空白
		return rapid.SampledFrom([]string{"", " ", "   ", "\t", "\n", " \t "}).Draw(t, "blank")
	case 1: // 错误协议前缀（归一化为小写后仍非 ws/wss）
		scheme := rapid.SampledFrom([]string{"http", "https", "ftp", "tcp", "file", "mqtt", "HTTP", "wsx", "sws", "w", "s"}).Draw(t, "badScheme")
		return scheme + "://" + host + "/mcp"
	case 2: // 缺少协议前缀
		return rapid.SampledFrom([]string{host, host + "/mcp", "//" + host, "//" + host + "/mcp"}).Draw(t, "noScheme")
	case 3: // 缺少主机名
		scheme := rapid.SampledFrom([]string{"ws", "wss"}).Draw(t, "emptyHostScheme")
		suffix := rapid.SampledFrom([]string{"://", ":///", ":///path", "://?token=abc"}).Draw(t, "emptyHostSuffix")
		return scheme + suffix
	default: // 非法 URL（空格等使解析失败）
		scheme := rapid.SampledFrom([]string{"ws", "wss"}).Draw(t, "malformedScheme")
		return scheme + "://" + "exa mple.com" + rapid.SampledFrom([]string{"", "/p ath", "/x^y"}).Draw(t, "malformedTail")
	}
}

// Feature: mcp-proxy-gateway, Property 29: 小智接入点地址协议校验
//
// Validates: Requirements 15.6
//
// 对任意字符串地址，ValidateEndpoint 满足 iff 关系：
//  1. 当地址为以 ws:// 或 wss:// 为前缀的合法 WebSocket URL（含非空主机名）时，校验
//     通过返回 nil（accept 分支）；
//  2. 否则拒绝保存，返回 domain.CodeValidation 类 APIError 且字段级错误包含
//     xiaozhi.endpoint（reject 分支）——指示地址格式无效（Req 15.6）。
func TestProperty29EndpointSchemeValidation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		if rapid.Bool().Draw(t, "wantValid") {
			endpoint := p29GenValidEndpoint(t)

			// 自洽性保险：确保生成器确实构造出 ws/wss 合法 URL（剔除偶发不满足构造前提的样本，
			// 避免误把生成器缺陷当作被测实现缺陷）。
			u, err := url.Parse(endpoint)
			if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
				t.Skipf("生成器产出非预期合法地址，跳过：%q", endpoint)
			}

			if got := ValidateEndpoint(endpoint); got != nil {
				t.Fatalf("合法 WebSocket 地址应被接受，但被拒绝：endpoint=%q err=%v", endpoint, got)
			}
			return
		}

		endpoint := p29GenInvalidEndpoint(t)

		got := ValidateEndpoint(endpoint)
		if got == nil {
			t.Fatalf("非法地址应被拒绝，但通过了校验：endpoint=%q", endpoint)
		}
		var apiErr *domain.APIError
		if !errors.As(got, &apiErr) {
			t.Fatalf("拒绝错误类型应为 *domain.APIError，实际为 %T：endpoint=%q", got, endpoint)
		}
		if apiErr.Code != domain.CodeValidation {
			t.Fatalf("拒绝错误码应为 VALIDATION，实际为 %s：endpoint=%q", apiErr.Code, endpoint)
		}
		if _, ok := apiErr.Fields[endpointField]; !ok {
			t.Fatalf("字段级错误应包含 %s，实际 Fields=%v：endpoint=%q", endpointField, apiErr.Fields, endpoint)
		}
	})
}
