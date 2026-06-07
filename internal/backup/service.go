package backup

import (
	"context"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// YAMLStore 抽象常规配置的读取与应用，由 config.Manager 实现。
//
// 抽象为接口以便 Service 单元测试在不触及文件系统的前提下注入内存实现。
type YAMLStore interface {
	// Config 返回当前 YAML 常规配置快照。
	Config() config.YAMLConfig
	// Save 校验并持久化 YAML 常规配置。
	Save(cfg config.YAMLConfig) error
}

// BusinessStore 抽象 PG 业务配置的导出与导入。
//
// ExportBusiness 读取库中全部业务配置；ImportBusiness 以备份内容整体替换库中业务配置。
// store 适配器（store_adapter.go）基于 PostgreSQL 仓储提供实现。
type BusinessStore interface {
	// ExportBusiness 读取并返回全部业务配置快照。
	ExportBusiness(ctx context.Context) (BusinessConfig, error)
	// ImportBusiness 以给定业务配置整体替换库中现有配置（先清空再写入）。
	ImportBusiness(ctx context.Context, bc BusinessConfig) error
}

// Service 是配置备份服务，编排导出与导入校验应用流程（Req 23.4、23.5、23.6）。
type Service struct {
	yaml     YAMLStore
	business BusinessStore
}

// NewService 构造备份服务；yaml 与 business 均不可为空。
func NewService(yaml YAMLStore, business BusinessStore) *Service {
	return &Service{yaml: yaml, business: business}
}

// Export 生成包含当前配置的可导入备份文件字节（Req 23.4）。
//
// 流程：读取 YAML 常规配置与 PG 业务配置，组装为 Backup 后序列化为 JSON 字节。
func (s *Service) Export(ctx context.Context) ([]byte, error) {
	if s.yaml == nil || s.business == nil {
		return nil, domain.NewError(domain.CodeValidation, "备份服务未正确装配")
	}

	bc, err := s.business.ExportBusiness(ctx)
	if err != nil {
		return nil, err
	}

	b := Backup{
		Version:  FormatVersion,
		YAML:     s.yaml.Config(),
		Business: bc,
	}
	return Marshal(b)
}

// Import 校验并应用一份配置备份文件（Req 23.5、23.6）。
//
// 流程：
//  1. 解析备份文件字节；格式非法返回备份无效错误（Req 23.6）。
//  2. 校验版本、YAML 配置与业务配置；校验失败返回备份无效错误（Req 23.6）。
//  3. 先应用业务配置（整体替换），再保存 YAML 常规配置（Req 23.5）。
//
// 注意：仅在校验全部通过后才开始应用，避免对非法备份产生部分写入。
func (s *Service) Import(ctx context.Context, data []byte) error {
	if s.yaml == nil || s.business == nil {
		return domain.NewError(domain.CodeValidation, "备份服务未正确装配")
	}

	b, err := ParseAndValidate(data)
	if err != nil {
		return err
	}

	// 先应用业务配置（整体替换库中现有上游/规则/API Key）。
	if err := s.business.ImportBusiness(ctx, b.Business); err != nil {
		return err
	}

	// 再保存 YAML 常规配置（Save 内部会再次做范围校验）。
	if err := s.yaml.Save(b.YAML); err != nil {
		return err
	}
	return nil
}
