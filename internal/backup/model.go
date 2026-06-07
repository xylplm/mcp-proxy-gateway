package backup

import (
	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// FormatVersion 为备份文件的格式版本号；导入时据此识别不兼容的备份格式（Req 23.6）。
const FormatVersion = "mpg-backup/v1"

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

// BusinessConfig 是 PG 业务配置的可序列化快照，覆盖上游 MCP、其从属规则与 API Key 元数据。
//
// 该结构同时用作 BusinessStore 的导出/导入载体：导出时由仓储读取填充，
// 导入时整体替换库中现有业务配置。
type BusinessConfig struct {
	// Upstreams 为全部上游 MCP 及其从属的别名规则、MCP 级屏蔽规则。
	Upstreams []UpstreamEntry `json:"upstreams"`
	// APIKeys 为全部 API Key 元数据及其从属的屏蔽规则与来源白名单。
	APIKeys []APIKeyEntry `json:"apiKeys"`
}

// UpstreamEntry 为单个上游 MCP 及其从属规则的备份条目。
type UpstreamEntry struct {
	// ID 为上游 MCP 标识；保留以维持别名/屏蔽规则的归属关系与导入导出等价性。
	ID string `json:"id"`
	// Config 为上游配置（不含明文凭证，明文从不持久化）。
	Config domain.UpstreamConfig `json:"config"`
	// CredentialEnc 为加密后的鉴权凭证字节；无凭证时为 nil（JSON 中编码为 base64）。
	CredentialEnc []byte `json:"credentialEnc,omitempty"`
	// AliasRules 为绑定在该上游上的别名规则。
	AliasRules []domain.AliasRule `json:"aliasRules"`
	// FilterRules 为绑定在该上游上的 MCP 级屏蔽规则。
	FilterRules []domain.FilterRule `json:"filterRules"`
}

// APIKeyEntry 为单个 API Key 元数据及其从属配置的备份条目。
type APIKeyEntry struct {
	// Meta 为 API Key 元数据（仅哈希与前缀，永不含明文，Req 12.3）。
	Meta store.APIKey `json:"meta"`
	// FilterRules 为绑定在该 API Key 上的屏蔽规则。
	FilterRules []domain.FilterRule `json:"filterRules"`
	// ACLCIDRs 为该 API Key 的来源白名单 CIDR 列表。
	ACLCIDRs []string `json:"aclCidrs"`
}
