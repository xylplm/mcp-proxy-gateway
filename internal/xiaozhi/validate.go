package xiaozhi

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件（任务 21.2）实现小智接入点地址校验：仅接受以 ws:// 或 wss:// 为协议前缀的合法
// WebSocket URL（Req 15.6）。校验为纯函数，便于属性测试（任务 21.3 / Property 29）与
// 在更新配置前据其拒绝非法地址。
//
// 与 config 层 ValidateYAMLConfig 中的端点校验语义保持一致（同一规则的两处守卫：持久化层
// 与连接服务层），但本函数返回携带字段级说明的 domain.APIError，便于管理 API 直接回传。

// endpointField 为地址校验错误对应的字段名，供前端定位无效字段。
const endpointField = "xiaozhi.endpoint"

// ValidateEndpoint 校验小智接入点地址是否为 ws:// 或 wss:// 合法 WebSocket URL（Req 15.6）。
//
// 合法条件（全部满足）：
//   - 非空（去除首尾空白后）；
//   - 可被解析为合法 URL；
//   - 协议（scheme）为 ws 或 wss（区分大小写，标准 URL scheme 解析后为小写）；
//   - 含非空主机名（host）。
//
// 任一不满足则返回 CodeValidation 类 APIError（字段 xiaozhi.endpoint），指示地址格式无效；
// 校验通过返回 nil。
func ValidateEndpoint(endpoint string) error {
	if msg, ok := endpointError(endpoint); !ok {
		return domain.NewValidationError("小智接入点地址格式无效", map[string]string{endpointField: msg})
	}
	return nil
}

// endpointError 返回地址非法时的说明与是否合法，供 ValidateEndpoint 复用并保持判定集中。
func endpointError(endpoint string) (string, bool) {
	if strings.TrimSpace(endpoint) == "" {
		return "接入点地址不能为空", false
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Sprintf("接入点地址不是合法 URL：%v", err), false
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return "接入点地址协议必须为 ws:// 或 wss://", false
	}
	if u.Host == "" {
		return "接入点地址缺少主机名", false
	}
	return "", true
}
