// Command gateway 是 MCP Proxy Gateway 的可执行入口。
//
// 该入口负责装配配置管理、数据层、加密服务、各应用服务、领域核心与入站路由，
// 并启动 HTTP/WS 服务对外提供管理界面与聚合 MCP 能力。具体的组件接线与生命周期
// 管理由 internal/app 包完成（任务 27.2）；main 仅负责构造日志、监听系统信号以驱动
// 优雅停机，并在启动致命错误时记录日志并以非零码退出（Req 18.3/18.6、19.4、20.1）。
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/myGithub/mcp-proxy-gateway/internal/app"
	"github.com/myGithub/mcp-proxy-gateway/internal/syslog"
)

func main() {
	systemLogs := syslog.NewStore(2000)
	console := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(syslog.NewHandler(console, systemLogs))
	slog.SetDefault(logger)

	// 监听 SIGINT/SIGTERM，触发上下文取消以驱动优雅停机。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 装配整个系统：配置 → DB/Redis/迁移 → 加密 → 各服务 → 领域核心 → 入站路由。
	// 启动致命错误（缺失/非法环境变量、YAML 非法、加密密钥无效、迁移/连接失败）在此终止。
	for ctx.Err() == nil {
		application, err := app.New(ctx, logger, app.WithSystemLogs(systemLogs))
		if err != nil {
			logger.Error("startup failed", "error", err)
			os.Exit(1)
		}

		if err := application.Run(ctx); errors.Is(err, app.ErrRestart) {
			logger.Info("restart app with latest settings")
			continue
		} else if err != nil {
			logger.Error("service runtime failed", "error", err)
			os.Exit(1)
		}
		return
	}
}
