// Command gateway 是 MCP Proxy Gateway 的可执行入口。
//
// 该入口负责装配配置管理、数据层、加密服务、各应用服务、领域核心与入站路由，
// 并启动 HTTP/WS 服务对外提供管理界面与聚合 MCP 能力。具体的组件接线与生命周期
// 管理由 internal/app 包完成（任务 27.2）；main 仅负责构造日志、监听系统信号以驱动
// 优雅停机，并在启动致命错误时记录日志并以非零码退出（Req 18.3/18.6、19.4、20.1）。
package main

// version 在 CI 构建时通过 -ldflags "-X 'main.version=1.0.xxxxx'" 注入，
// 本地构建默认为 "dev"。
var version = "dev"

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
	// 使用可变日志级别变量，使 ApplySettings 能在运行时按配置即时切换级别。
	// 初始为 Info；app.New 加载 YAML 后会通过 ApplySettings/首启校正为配置值。
	// AddSource 让 stdout JSON 输出携带调用方源码位置（file/line/function），
	// 便于在容器日志中定位触发点；syslog.Handler 独立解析 PC 供管理台展示。
	levelVar := &slog.LevelVar{}
	levelVar.Set(slog.LevelInfo)
	console := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: levelVar, AddSource: true})
	logger := slog.New(syslog.NewHandler(console, systemLogs))
	slog.SetDefault(logger)

	// 监听 SIGINT/SIGTERM，触发上下文取消以驱动优雅停机。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 装配整个系统：配置 → DB/Redis/迁移 → 加密 → 各服务 → 领域核心 → 入站路由。
	// 启动致命错误（缺失/非法环境变量、YAML 非法、加密密钥无效、迁移/连接失败）在此终止。
	for ctx.Err() == nil {
		application, err := app.New(ctx, logger, app.WithSystemLogs(systemLogs), app.WithLevelVar(levelVar))
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
