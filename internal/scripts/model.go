// Package scripts 提供管理台「脚本中心」的受管脚本资产：
// 文件仓存储、版本、静态风险分析与启动参数解析。
//
// 安全边界：
//   - 脚本仅落在 {dataDir}/scripts/library 内；
//   - 执行仍通过 runtime 白名单解释器 + args 传脚本绝对路径，禁止 shell；
//   - 本包不做任意代码执行，analyze 仅为静态规则扫描。
package scripts

import "time"

// Language 为脚本语言（决定默认解释器与高亮）。
type Language string

const (
	LangPython     Language = "python"
	LangJavaScript Language = "javascript"
)

// RiskLevel 静态风险等级。
type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

// Status 脚本生命周期状态。
type Status string

const (
	StatusActive Status = "active"
	StatusTrash  Status = "trash"
)

// Finding 为单条风险发现。
type Finding struct {
	Rule     string    `json:"rule"`
	Severity RiskLevel `json:"severity"`
	Line     int       `json:"line,omitempty"`
	Message  string    `json:"message"`
}

// RiskReport 为静态分析结果。
type RiskReport struct {
	Level    RiskLevel `json:"level"`
	Score    int       `json:"score"`
	Findings []Finding `json:"findings"`
}

// Script 为脚本资产元数据（列表/详情）。
type Script struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Description    string     `json:"description,omitempty"`
	Language       Language   `json:"language"`
	Runtime        string     `json:"runtime"` // node | python3 | ...
	EntryFile      string     `json:"entryFile"`
	Tags           []string   `json:"tags,omitempty"`
	Status         Status     `json:"status"`
	CurrentVersion string     `json:"currentVersion"`
	ContentSHA256  string     `json:"contentSha256,omitempty"`
	Risk           RiskReport `json:"risk"`
	SizeBytes      int64      `json:"sizeBytes"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	// EntryPath 为当前版本在网关主机上的绝对路径，仅供服务端启动预览与内部解析。
	EntryPath string `json:"entryPath,omitempty"`
}

// VersionMeta 为不可变版本摘要。
type VersionMeta struct {
	Version       string     `json:"version"`
	ContentSHA256 string     `json:"contentSha256"`
	SizeBytes     int64      `json:"sizeBytes"`
	Risk          RiskReport `json:"risk"`
	Note          string     `json:"note,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	EntryFile     string     `json:"entryFile"`
}

// ScriptDetail 含当前内容。
type ScriptDetail struct {
	Script
	Content string `json:"content"`
}

// CreateInput 新建脚本。
type CreateInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Language    Language `json:"language"`
	Runtime     string   `json:"runtime"`
	Content     string   `json:"content"`
	Tags        []string `json:"tags"`
	Note        string   `json:"note"`
}

// UpdateMetaInput 更新名称/描述/标签（不产生版本）。
type UpdateMetaInput struct {
	Name        *string  `json:"name"`
	Description *string  `json:"description"`
	Tags        []string `json:"tags"`
}

// SaveContentInput 发布新版本内容。
type SaveContentInput struct {
	Content string `json:"content"`
	Note    string `json:"note"`
}

// DiffResult 为两边文本 diff（行级摘要）。
type DiffResult struct {
	LeftLabel  string   `json:"leftLabel"`
	RightLabel string   `json:"rightLabel"`
	Hunks      []string `json:"hunks"`
	Truncated  bool     `json:"truncated"`
}

// LaunchBinding 上游 connParams.scriptRef 结构。
type LaunchBinding struct {
	ScriptID      string    `json:"scriptId"`
	Version       string    `json:"version"`
	EntryFile     string    `json:"entryFile"`
	ContentSHA256 string    `json:"contentSha256"`
	Runtime       string    `json:"runtime"`
	EntryPath     string    `json:"entryPath"`
	RiskLevel     RiskLevel `json:"riskLevel"`
	RiskScore     int       `json:"riskScore"`
}
