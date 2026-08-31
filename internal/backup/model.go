package backup

import (
	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/risk"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// FormatVersion 为备份文件的格式版本号；导入时据此识别不兼容的备份格式（Req 23.6）。
const FormatVersion = "mpg-backup/v2"

// Backup 是一份完整的可导入配置备份（Req 23.4）。
//
// 它聚合 YAML 常规配置与 PG 业务配置两部分，序列化为 JSON 字节即为导出的备份文件；
// 反序列化并通过 Validate 校验后即可应用到系统（Req 23.5）。
type Backup struct {
	// Version 为备份格式版本，必须等于 FormatVersion 才被接受导入。
	Version string `json:"version"`
	// YAML 为常规配置（config.YAMLConfig）。
	YAML config.YAMLConfig `json:"yaml"`
	// Business 为 PG 业务配置（上游/规则/API Key 元数据）。
	Business BusinessConfig `json:"business"`
}

// BusinessConfig 是 PG 业务配置的可序列化快照，覆盖上游 MCP、独立规则与 API Key 元数据。
//
// 该结构同时用作 BusinessStore 的导出/导入载体：导出时由仓储读取填充，
// 导入时整体替换库中现有业务配置。
type BusinessConfig struct {
	// Upstreams 为全部上游 MCP。
	Upstreams []UpstreamEntry `json:"upstreams"`
	// AliasRules 为独立管理的全部别名规则，规则自身携带作用范围。
	AliasRules []domain.AliasRule `json:"aliasRules,omitempty"`
	// MCPFilterRules 为独立管理的全部 MCP 级屏蔽规则，规则自身携带作用范围。
	MCPFilterRules []domain.FilterRule `json:"mcpFilterRules,omitempty"`
	// ToolPolicyRules 为独立管理的全部工具策略规则。
	ToolPolicyRules []domain.ToolPolicyRule `json:"toolPolicyRules,omitempty"`
	// APIKeys 为全部 API Key 元数据及其从属的屏蔽规则与来源白名单。
	APIKeys []APIKeyEntry `json:"apiKeys"`
	// AIProviders 包含 Provider 的完整配置与明文 API Key；备份文件应按敏感凭据保护。
	AIProviders []risk.Provider `json:"aiProviders,omitempty"`
	// ToolRisks 保留风险目录与人工覆盖历史。
	ToolRisks []risk.Assessment `json:"toolRisks,omitempty"`
}

// UpstreamEntry 为单个上游 MCP 的备份条目。
type UpstreamEntry struct {
	// ID 为上游 MCP 标识；保留以维持别名/屏蔽规则的归属关系与导入导出等价性。
	ID string `json:"id"`
	// Config 为上游配置；凭证明文随 Config.Credential 一并备份（自部署场景，明文存储）。
	Config domain.UpstreamConfig `json:"config"`
}

// APIKeyEntry 为单个 API Key 元数据及其从属配置的备份条目。
type APIKeyEntry struct {
	// Meta 为 API Key 元数据（含明文密钥 KeyPlain，与运行态一致；鉴权仍走 KeyHash）。
	// 备份文件因此等价于明文密钥载体，导出/传输时需按密钥对待。
	Meta store.APIKey `json:"meta"`
	// FilterRules 为绑定在该 API Key 上的屏蔽规则。
	FilterRules []domain.FilterRule `json:"filterRules"`
	// ACLCIDRs 为该 API Key 的来源白名单 CIDR 列表。
	ACLCIDRs []string `json:"aclCidrs"`
	// UpstreamIDs 为 selected 模式下允许访问的上游标识。
	UpstreamIDs []string `json:"upstreamIds,omitempty"`
}
