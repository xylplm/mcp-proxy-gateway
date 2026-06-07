package domain

import (
	"context"
	"encoding/json"
	"time"
)

// Aggregation_Service 是聚合服务接口，编排聚合管线并负责工具调用路由。
//
// 接口名沿用设计文档「关键接口契约」中的命名。
type Aggregation_Service interface {
	// BuildToolSet 构建某 API Key 视角的可见聚合工具集合（执行完整管线）。
	BuildToolSet(ctx context.Context, apiKeyID string) ([]ToolDef, error)
	// InvokeTool 调用聚合工具：别名反向映射 → 路由到上游 → 原样返回结果。
	InvokeTool(ctx context.Context, apiKeyID, exposedName string, args json.RawMessage) (ToolResult, error)
}

// Tool_Cache 是工具缓存接口，聚合服务永不实时拉取上游，只从缓存读取。
//
// 接口名沿用设计文档「关键接口契约」中的命名。
type Tool_Cache interface {
	// Get 读取某上游 MCP 最近一次持久化的工具列表及其更新时间。
	Get(ctx context.Context, upstreamID string) ([]ToolDef, time.Time, bool)
	// Replace 以整列表替换语义写入某上游 MCP 的工具列表。
	Replace(ctx context.Context, upstreamID string, tools []ToolDef) error
	// Delete 删除某上游 MCP 的缓存工具列表。
	Delete(ctx context.Context, upstreamID string) error
}
