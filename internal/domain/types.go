// Package domain 定义网关系统的核心领域类型与接口契约。
//
// 该包位于分层架构的领域核心层，被传输适配、连接管理、聚合管线等多个
// 上层包共同依赖；为避免循环依赖，所有跨层共享的基础类型集中定义于此。
package domain

import (
	"encoding/json"
	"time"
)

// TransportType 表示上游 MCP 服务的传输类型。
type TransportType string

const (
	// TransportStdio 表示通过标准输入输出与子进程形式的上游 MCP 通信。
	TransportStdio TransportType = "stdio"
	// TransportSSE 表示通过 Server-Sent Events 与上游 MCP 通信。
	TransportSSE TransportType = "sse"
	// TransportStreamableHTTP 表示通过 Streamable-HTTP 与上游 MCP 通信。
	TransportStreamableHTTP TransportType = "streamable-http"
	// TransportWebSocket 表示通过 WebSocket 与上游 MCP 通信。
	TransportWebSocket TransportType = "websocket"
)

// ConnState 表示上游 MCP 连接的生命周期状态。
type ConnState string

const (
	// ConnConnecting 表示连接正在建立或正在完成 MCP 初始化握手。
	ConnConnecting ConnState = "connecting"
	// ConnAvailable 表示连接可用并可正常提供工具。
	ConnAvailable ConnState = "available"
	// ConnUnavailable 表示连接当前不可用（断开或建立失败）。
	ConnUnavailable ConnState = "unavailable"
	// ConnSuspended 表示连续失败达到阈值后暂停自动重试。
	ConnSuspended ConnState = "suspended"
)

// ToolDef 是聚合管线中流动的核心工具定义。
type ToolDef struct {
	// OriginalName 为上游原始名称，是工具调用路由的依据。
	OriginalName string `json:"originalName"`
	// Name 为对外暴露名称，可被别名规则或同名去重改写。
	Name string `json:"name"`
	// Description 为对外暴露的工具描述。
	Description string `json:"description"`
	// InputSchema 为工具入参的 JSON Schema 原始字节。
	InputSchema json.RawMessage `json:"inputSchema"`
	// UpstreamID 标识该工具所属的上游 MCP 服务。
	UpstreamID string `json:"upstreamId"`
	// Order 继承所属上游 MCP 的排序顺序。
	Order int `json:"order"`
	// SourceCount 表示该对外工具当前可路由的来源上游数量，仅用于管理台展示。
	SourceCount int `json:"sourceCount,omitempty"`
	// SchemaConflict 表示同名来源中存在与对外展示 schema 不一致的工具，仅用于管理台提示。
	SchemaConflict bool `json:"schemaConflict,omitempty"`
}

// ToolResult 表示上游 MCP 工具调用返回的结果，无论成功或上游报告的错误均原样透传。
type ToolResult struct {
	// IsError 表示该结果是否为上游报告的错误结果。
	IsError bool `json:"isError"`
	// Content 为工具调用结果内容（MCP content 数组的原始 JSON）。
	Content json.RawMessage `json:"content"`
}

// ToolSourceView 是管理台展示某个对外工具来源上游的只读视图。
type ToolSourceView struct {
	UpstreamID     string             `json:"upstreamId"`
	UpstreamName   string             `json:"upstreamName"`
	OriginalName   string             `json:"originalName"`
	Description    string             `json:"description"`
	InputSchema    json.RawMessage    `json:"inputSchema"`
	Compatible     bool               `json:"compatible"`
	SchemaConflict bool               `json:"schemaConflict"`
	RateLimits     UpstreamRateLimits `json:"rateLimits,omitempty"`
}

// ToolDetail 是管理台工具列表的只读详情视图。
type ToolDetail struct {
	Tool    ToolDef          `json:"tool"`
	Sources []ToolSourceView `json:"sources"`
}

// UpstreamConfig 表示上游 MCP 服务的配置。
type UpstreamConfig struct {
	// Name 为服务名称，长度需在 1 至 100 个字符之间。
	Name string `json:"name"`
	// Tags 为用户自定义标签，用于在管理台中分组与识别上游 MCP。
	Tags []string `json:"tags,omitempty"`
	// Transport 为传输类型。
	Transport TransportType `json:"transport"`
	// ConnParams 为传输类型相关的连接参数。
	ConnParams map[string]any `json:"connParams"`
	// Credential 为鉴权凭证明文。自部署场景下明文存储，便于在管理台编辑回显与复制；
	// 仅在内存与持久层流转，连接拨号时直接使用。
	Credential string `json:"credential"`
	// Enabled 表示该上游是否启用并参与聚合。
	Enabled bool `json:"enabled"`
	// SortOrder 为该上游在列表与聚合中的排序顺序。
	SortOrder int `json:"sortOrder"`
	// AutoSync 表示是否对该上游开启工具列表自动同步。
	AutoSync bool `json:"autoSync"`
	// RateLimits 为该上游的调用频率与周期额度限制；未启用时不参与调用路由判定。
	RateLimits UpstreamRateLimits `json:"rateLimits,omitempty"`
}

// UpstreamRateLimits 表示上游 MCP 的本地调用频率与周期额度限制。
//
// 所有限额均为「单上游实例」维度；取值 <=0 表示该维度不限额。多个维度同时配置时需
// 同时满足，例如每分钟 60 且每天 1000 表示两条约束都会生效。
type UpstreamRateLimits struct {
	Enabled   bool   `json:"enabled"`
	PerSecond int    `json:"perSecond,omitempty"`
	PerMinute int    `json:"perMinute,omitempty"`
	PerHour   int    `json:"perHour,omitempty"`
	PerDay    int    `json:"perDay,omitempty"`
	PerWeek   int    `json:"perWeek,omitempty"`
	PerMonth  int    `json:"perMonth,omitempty"`
	Timezone  string `json:"timezone,omitempty"`
}

// ToolRoutingStrategy 表示同名工具有多个来源上游时的内部调用选择策略。
type ToolRoutingStrategy string

const (
	// ToolRoutingPriorityFill 按上游排序优先选择第一个健康且未超额的来源。
	ToolRoutingPriorityFill ToolRoutingStrategy = "priority_fill"
	// ToolRoutingRoundRobin 在同名来源之间轮询分配调用。
	ToolRoutingRoundRobin ToolRoutingStrategy = "round_robin"
)

// ValidToolRoutingStrategy 判断工具调用策略是否为受支持取值。
func ValidToolRoutingStrategy(s ToolRoutingStrategy) bool {
	switch s {
	case ToolRoutingPriorityFill, ToolRoutingRoundRobin:
		return true
	default:
		return false
	}
}

// Upstream 表示已持久化的上游 MCP 服务实例及其运行期状态。
type Upstream struct {
	// ID 为上游 MCP 服务的唯一标识。
	ID string `json:"id"`
	// Config 为该上游的配置。
	Config UpstreamConfig `json:"config"`
	// State 为该上游当前的连接状态。
	State ConnState `json:"state"`
	// LastError 为最近一次连接失败的原因（如有）。
	LastError string `json:"lastError,omitempty"`
	// CreatedAt 为创建时间。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 为最近更新时间。
	UpdatedAt time.Time `json:"updatedAt"`
}

// AliasRule 是用于重命名工具或重写描述的规则，可作用于全部上游或指定多个上游。
type AliasRule struct {
	// ID 为规则唯一标识。
	ID string `json:"id"`
	// ScopeType 为规则作用范围：all 或 upstreams。
	ScopeType string `json:"scopeType,omitempty"`
	// UpstreamIDs 为 scopeType=upstreams 时指定的上游 MCP 标识列表。
	UpstreamIDs []string `json:"upstreamIds,omitempty"`
	// Pattern 为匹配模式，长度需在 1 至 200 个字符之间。
	Pattern string `json:"pattern"`
	// IsRegex 表示是否启用正则匹配（完整匹配）。
	IsRegex bool `json:"isRegex"`
	// TargetName 为目标名称，长度 1 至 100，与目标描述至少提供其一。
	TargetName string `json:"targetName,omitempty"`
	// TargetDesc 为目标描述，长度不超过 1024。
	TargetDesc string `json:"targetDesc,omitempty"`
	// SortOrder 为规则排序顺序，多规则匹配时仅应用首条。
	SortOrder int `json:"sortOrder"`
}

// FilterRule 是用于屏蔽过滤工具的规则，可作用于全部上游、指定多个上游或 API Key。
type FilterRule struct {
	// ID 为规则唯一标识。
	ID string `json:"id"`
	// Pattern 为匹配模式，长度需在 1 至 200 个字符之间。
	Pattern string `json:"pattern"`
	// IsRegex 表示是否启用正则匹配（完整匹配）。
	IsRegex bool `json:"isRegex"`
	// Enabled 表示该规则是否启用，支持单条启停。
	Enabled bool `json:"enabled"`
	// SortOrder 为规则排序顺序。
	SortOrder int `json:"sortOrder"`
	// ScopeType 为规则作用范围：all 或 upstreams。API Key 级规则由独立服务管理。
	ScopeType string `json:"scopeType,omitempty"`
	// UpstreamIDs 为 scopeType=upstreams 时指定的上游 MCP 标识列表。
	UpstreamIDs []string `json:"upstreamIds,omitempty"`
}
