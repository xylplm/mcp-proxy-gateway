package app

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestShutdownCauseDescribesSignal 验证停机原因的三种取值。
//
// 这里用 context.WithCancelCause 直接模拟，而不去真发信号：signal.NotifyContext 内部正是
// 以 cause 形式携带信号（Go 1.26 起），而在测试进程里给自己发 SIGINT 会直接终止测试。
// 被测对象是 shutdownCause 的归类逻辑，信号投递本身属标准库职责。
func TestShutdownCauseDescribesSignal(t *testing.T) {
	// 1) 信号触发：原样透出信号描述，便于区分 SIGTERM（编排系统）与 SIGINT（人工中断）。
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("interrupt signal received"))
	if got := shutdownCause(ctx); got != "interrupt signal received" {
		t.Errorf("信号触发时应透出信号描述，实际 %q", got)
	}

	// 2) 非信号路径（调用 stop 或父上下文结束）：cause 为 context.Canceled。
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if got := shutdownCause(ctx2); got != "上下文取消" {
		t.Errorf("普通取消应归类为上下文取消，实际 %q", got)
	}

	// 3) 尚未取消：无原因可报。
	if got := shutdownCause(context.Background()); got != "未知" {
		t.Errorf("未取消的上下文应返回未知，实际 %q", got)
	}
}

// TestHTTPServersSetHeaderCountLimits 验证两个监听端口都设置了 header 数量上限，
// 且对外端口不宽于管理端口。
//
// MaxHeaderBytes 只约束请求头总字节数，挡不住「大量极小 header」；这条上限是对它的补充，
// 属于对外暴露面的加固项，故用测试锁定，避免日后重构时被漏掉。
func TestHTTPServersSetHeaderCountLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &App{
		adminAddr:       ":18080",
		adminEngine:     gin.New(),
		publicMCPAddr:   ":18081",
		publicMCPEngine: gin.New(),
	}

	servers := a.httpServers()
	if len(servers) != 2 {
		t.Fatalf("期望装配管理与对外 MCP 两个服务，实际 %d 个", len(servers))
	}

	for _, spec := range servers {
		if spec.server.MaxHeaderValueCount <= 0 {
			t.Errorf("%s 服务应设置 header 数量上限，实际 %d", spec.name, spec.server.MaxHeaderValueCount)
		}
	}

	if a.publicMCPServer.MaxHeaderValueCount > a.adminServer.MaxHeaderValueCount {
		t.Errorf("对外 MCP 端口的 header 数量上限不应宽于管理端口：对外 %d > 管理 %d",
			a.publicMCPServer.MaxHeaderValueCount, a.adminServer.MaxHeaderValueCount)
	}

	// 上限应显著低于标准库默认值，否则等于没有收紧。
	if a.publicMCPServer.MaxHeaderValueCount >= http.DefaultMaxHeaderValueCount {
		t.Errorf("对外 MCP 端口上限 %d 未低于标准库默认 %d，未起到收紧作用",
			a.publicMCPServer.MaxHeaderValueCount, http.DefaultMaxHeaderValueCount)
	}
}
