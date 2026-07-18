package transport

import (
	"sync"

	"github.com/myGithub/mcp-proxy-gateway/internal/runtime"
)

// PolicyProvider 返回当前 stdio 运行时策略；由 app 层注入，热读取配置快照。
type PolicyProvider func() runtime.Policy

var (
	policyMu       sync.RWMutex
	policyProvider PolicyProvider
)

// SetPolicyProvider 注册全局策略提供者（进程内单一网关实例）。
// 传 nil 表示恢复默认宽松兼容策略（stdio 启用 + 默认白名单）。
func SetPolicyProvider(p PolicyProvider) {
	policyMu.Lock()
	policyProvider = p
	policyMu.Unlock()
}

func currentPolicy() runtime.Policy {
	policyMu.RLock()
	p := policyProvider
	policyMu.RUnlock()
	if p == nil {
		return runtime.DefaultPolicy()
	}
	return runtime.NormalizePolicy(p())
}
