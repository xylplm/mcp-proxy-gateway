package httpapi

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
)

// 本文件提供审计事件异步记录的便捷封装，供各管理 handler 在主操作成功后调用。
//
// 设计要点（对齐审计"异步+吞错"策略）：
//   - 所有封装均为 nil 安全：当 r.auditRecorder 未注入（渐进接线或测试）时静默跳过，
//     不影响 handler 主流程，也不需要每个调用点重复判空。
//   - 调用 auditRecorder.Record* 后忽略返回的错误（异步入队恒返回 nil，且落库失败
//     由 Recorder 内部静默丢弃并记日志），绝不把审计失败传导给业务响应。
//   - 上下文取请求上下文（gin.Context 派生），与请求生命周期对齐。
//
// 事件语义与 ResourceKind 映射见 internal/audit/service.go；本层只做接线，不持有业务逻辑。

// recordLogin 记录一次管理员登录事件（成功或失败），target 为登录用户名。
func (r *Router) recordLogin(c *gin.Context, username string, success bool) {
	if r.auditRecorder == nil {
		return
	}
	_ = r.auditRecorder.RecordLogin(c.Request.Context(), username, success)
}

// recordCreate 记录一次资源创建事件。
func (r *Router) recordCreate(c *gin.Context, kind audit.ResourceKind, target string) {
	if r.auditRecorder == nil {
		return
	}
	_ = r.auditRecorder.RecordCreate(c.Request.Context(), kind, target)
}

// recordUpdate 记录一次资源更新事件。
//
// 语义上覆盖更新及近似的写操作（启停、重排序、重连、设置保存、改密等），
// 通过 kind/detail 区分资源类别，不新增事件类型枚举。
func (r *Router) recordUpdate(c *gin.Context, kind audit.ResourceKind, target string) {
	if r.auditRecorder == nil {
		return
	}
	_ = r.auditRecorder.RecordUpdate(context.Background(), kind, target)
}

// recordDelete 记录一次资源删除事件。
func (r *Router) recordDelete(c *gin.Context, kind audit.ResourceKind, target string) {
	if r.auditRecorder == nil {
		return
	}
	_ = r.auditRecorder.RecordDelete(c.Request.Context(), kind, target)
}
