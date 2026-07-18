package transport

import (
	"sync"

	"github.com/myGithub/mcp-proxy-gateway/internal/runtime"
)

// PolicyProvider 返回当前 stdio 运行时策略；由 app 层注入，热读取配置快照。
type PolicyProvider func() runtime.Policy

// RuntimeDirProvider 返回卷内运行时根目录；由 app 层注入。
type RuntimeDirProvider func() string

var (
	policyMu       sync.RWMutex
	policyProvider PolicyProvider

	runtimeDirMu       sync.RWMutex
	runtimeDirProvider RuntimeDirProvider
)

// SetPolicyProvider 注册全局策略提供者（进程内单一网关实例）。
// 传 nil 表示恢复默认宽松兼容策略（stdio 启用 + 默认白名单）。
func SetPolicyProvider(p PolicyProvider) {
	policyMu.Lock()
	policyProvider = p
	policyMu.Unlock()
}

// SetRuntimeDirProvider 注册运行时目录提供者；传 nil 表示无卷路径增强。
func SetRuntimeDirProvider(p RuntimeDirProvider) {
	runtimeDirMu.Lock()
	runtimeDirProvider = p
	runtimeDirMu.Unlock()
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

func currentRuntimeDir() string {
	runtimeDirMu.RLock()
	p := runtimeDirProvider
	runtimeDirMu.RUnlock()
	if p == nil {
		return ""
	}
	return p()
}

func currentPathPrefixes() []string {
	return runtime.PathPrefixes(currentRuntimeDir())
}
