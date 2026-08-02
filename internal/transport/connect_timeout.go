package transport

import (
	"sync"
	"time"
)

// ConnectTimeoutProvider 返回当前全局上游连接建立超时。连接超时属于网关级设置，
// 通过提供器避免把 config 包引入传输层，也让新建会话始终读取最新运行态快照。
type ConnectTimeoutProvider func() time.Duration

var (
	connectTimeoutMu       sync.RWMutex
	connectTimeoutProvider ConnectTimeoutProvider
)

// SetConnectTimeoutProvider 注册连接建立超时提供器；传 nil 时回退默认值。
func SetConnectTimeoutProvider(provider ConnectTimeoutProvider) {
	connectTimeoutMu.Lock()
	connectTimeoutProvider = provider
	connectTimeoutMu.Unlock()
}

func currentConnectTimeout() time.Duration {
	connectTimeoutMu.RLock()
	provider := connectTimeoutProvider
	connectTimeoutMu.RUnlock()
	if provider == nil {
		return DefaultConnectTimeout
	}
	timeout := provider()
	if timeout <= 0 {
		return DefaultConnectTimeout
	}
	return timeout
}
