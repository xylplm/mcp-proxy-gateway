package domain

import "context"

// MCP_Manager 是连接管理器接口，负责上游 MCP 的 CRUD、启停、排序与连接生命周期。
//
// 接口名沿用设计文档「关键接口契约」中的命名。
type MCP_Manager interface {
	// Create 创建上游 MCP 服务。
	Create(ctx context.Context, cfg UpstreamConfig) (Upstream, error)
	// Update 更新上游 MCP 服务配置并按新配置重建连接。
	Update(ctx context.Context, id string, cfg UpstreamConfig) (Upstream, error)
	// Delete 删除上游 MCP 服务并级联清理工具缓存与规则。
	Delete(ctx context.Context, id string) error
	// Reorder 提交新的排序顺序，并校验其为已注册标识的恰好一次排列。
	Reorder(ctx context.Context, orderedIDs []string) error
	// SetEnabled 启用或停用某个上游 MCP 服务。
	SetEnabled(ctx context.Context, id string, enabled bool) error
	// GetState 返回连接状态与最近一次失败原因。
	GetState(id string) (ConnState, string)
	// Reconnect 由管理员手动发起重连。
	Reconnect(ctx context.Context, id string) error
}
