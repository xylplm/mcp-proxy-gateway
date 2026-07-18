package transport

import (
	"sync"

	"github.com/myGithub/mcp-proxy-gateway/internal/runtime"
	"github.com/myGithub/mcp-proxy-gateway/internal/scripts"
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

	scriptMu      sync.RWMutex
	scriptService *scripts.Service
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

// SetScriptService 注册受管脚本解析服务；传 nil 禁用 scriptRef 启动增强。
func SetScriptService(s *scripts.Service) {
	scriptMu.Lock()
	scriptService = s
	scriptMu.Unlock()
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

func currentScriptService() *scripts.Service {
	scriptMu.RLock()
	s := scriptService
	scriptMu.RUnlock()
	return s
}
