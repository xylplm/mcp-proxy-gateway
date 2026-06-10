package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
)

// Run 先做启动连通性探测（Req 20.1），随后启动后台服务与 HTTP 服务对外提供服务，
// 并在 ctx 取消时优雅停机（Req 20.1 的「先探测再对外服务」顺序）。
//
// 探测仅记录结构化日志，不因单项依赖失败而拒绝启动——是否对外服务由运维据日志判断；
// 但若 PG/Redis 等关键依赖在 New 阶段就连接失败，启动早已终止（见 New）。
func (a *App) Run(ctx context.Context) error {
	// 1) 启动连通性探测：按序记录 PG/Redis/各启用上游/小智的连通性（Req 20.1-20.5）。
	report := a.prober.ProbeStartup(ctx)
	if report.AllOK() {
		a.logger.Info("启动连通性探测全部通过", "checks", len(report.Results))
	} else {
		a.logger.Warn("启动连通性探测存在失败项，仍继续对外服务", "failures", len(report.Failures()))
	}

	// 2) 启动后台服务（统计 worker/清理、审计清理、同步 cron、小智连接）。
	a.startBackground(ctx)

	// 3) 启动 HTTP 服务并等待 ctx 取消后优雅停机。
	a.server = &http.Server{Addr: a.addr, Handler: a.engine}

	errCh := make(chan error, 1)
	go func() {
		a.logger.Info("HTTP 服务开始监听", "addr", a.addr)
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		a.logger.Info("收到停机信号，开始优雅停机")
		a.shutdown()
		return nil
	case err := <-errCh:
		// HTTP 服务异常退出：停止后台服务并清理。
		a.shutdown()
		return err
	}
}

// startBackground 启动各后台服务（Req 7、16）。
func (a *App) startBackground(ctx context.Context) {
	// 恢复数据库中已有上游的连接状态机登记，确保进程重启后可直接重连与展示状态。
	if err := a.mgr.RestoreConnections(ctx); err != nil {
		a.logger.Error("恢复上游连接状态失败，后续启用或重连时将按需恢复", "error", err)
	}

	// 统计异步落库 worker 与保留期清理（Req 16.8、16.10）。
	a.statRecorder.Start(ctx)
	a.statCleaner.Start(ctx)

	// 同步 cron 调度：注册周期同步任务并启动调度器（Req 7）。
	syncCron := a.cfg.Config().Sync.Cron
	if err := a.scheduler.UpdateSchedule(syncCron, func() {
		runCtx, cancel := context.WithTimeout(context.Background(), a.syncTimeout)
		defer cancel()
		a.syncer.SyncEnabledAutoSync(runCtx)
	}); err != nil {
		a.logger.Error("注册同步 cron 调度失败，周期同步未启用", "cron", syncCron, "error", err)
	} else {
		a.scheduler.Start()
	}

	// 审计保留期清理：以 24 小时为周期独立运行（Req 22.5）。
	a.startAuditRetention(ctx)

	// 小智接入：启用时连出到接入点并持续按退避重连（Req 15）。
	if a.xiaozhiConn.Enabled() {
		if err := a.xiaozhiConn.Start(ctx); err != nil {
			a.logger.Error("启动小智接入连接失败", "error", err)
		}
	}
}

// ApplySettings 将已保存的 YAML 配置应用到当前运行中的进程组件。
//
// 配置落盘由 config.Manager 负责；此处只处理需要即时影响运行态的部分：
// 对外 MCP 模式影响后续新建连接，小智接入需要按新配置启停或重连。
func (a *App) ApplySettings(cfg config.YAMLConfig) error {
	if a.mcpService != nil {
		a.mcpService.Reconfigure(cfg.MCPAPI.Mode, cfg.MCPAPI.SmartDiscoveryLimit)
	}

	if a.xiaozhiConn == nil {
		return nil
	}
	wasEnabled := a.xiaozhiConn.Enabled()
	wasEndpoint := a.xiaozhiConn.Endpoint()
	running := a.xiaozhiConn.Running()
	restart := running && cfg.XiaoZhi.Enabled && cfg.XiaoZhi.Endpoint != wasEndpoint
	stop := running && (!cfg.XiaoZhi.Enabled || cfg.XiaoZhi.Endpoint != wasEndpoint)

	if stop {
		a.xiaozhiConn.Stop()
		running = false
	}

	if err := a.xiaozhiConn.Reconfigure(cfg.XiaoZhi.Endpoint, cfg.XiaoZhi.Enabled); err != nil {
		return err
	}

	if cfg.XiaoZhi.Enabled && (!running || !wasEnabled || restart) {
		if err := a.xiaozhiConn.Start(context.Background()); err != nil {
			return err
		}
	}
	return nil
}

// startAuditRetention 启动审计保留期清理循环：立即清理一次，随后每 24 小时清理一次（Req 22.5）。
func (a *App) startAuditRetention(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		// 启动即清理一次，使重启后及时回收超期记录。
		if _, err := a.auditSvc.Cleanup(ctx); err != nil {
			a.logger.Warn("审计日志保留期清理失败", "error", err)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := a.auditSvc.Cleanup(ctx); err != nil {
					a.logger.Warn("审计日志保留期清理失败", "error", err)
				}
			}
		}
	}()
}

// shutdown 优雅停止 HTTP 服务与全部后台服务，并释放基础设施连接。
func (a *App) shutdown() {
	// 1) 停止接收新请求：关闭 HTTP 服务，给在途请求收尾时间。
	if a.server != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := a.server.Shutdown(shutCtx); err != nil {
			a.logger.Warn("HTTP 服务优雅停机超时，强制关闭", "error", err)
			_ = a.server.Close()
		}
	}

	// 2) 停止后台服务。
	if a.xiaozhiConn != nil {
		a.xiaozhiConn.Stop()
	}
	if a.scheduler != nil {
		a.scheduler.Stop()
	}
	if a.statCleaner != nil {
		a.statCleaner.Stop()
	}
	if a.statRecorder != nil {
		a.statRecorder.Stop()
	}
	if a.mgr != nil {
		a.mgr.Shutdown()
	}

	// 3) 释放基础设施连接。
	if err := a.closeInfra(); err != nil {
		a.logger.Warn("释放基础设施连接时出错", "error", err)
	}
	a.logger.Info("已完成优雅停机")
}

// signingKey 由加密主密钥材料派生出 JWT 签名密钥（HMAC-SHA256）。
//
// 取主密钥字符串的 SHA-256 摘要作为签名密钥，使签名密钥不直接等同于加密密钥，
// 又无需引入额外的环境变量；进程重启后由相同主密钥派生的签名密钥保持稳定。
func signingKey(encryptionKey string) []byte {
	sum := sha256.Sum256([]byte("mpg-jwt:" + encryptionKey))
	return sum[:]
}

// probeXiaoZhi 探测小智接入点连通性：尝试建立一次 WebSocket 连接后立即关闭（Req 20.5）。
func probeXiaoZhi(ctx context.Context, endpoint string) error {
	conn, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		return err
	}
	_ = conn.Close(websocket.StatusNormalClosure, "probe done")
	return nil
}
