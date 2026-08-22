package store

import (
	"context"
	"errors"
	"strings"
	"uuid"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// PostgreSQL 错误码（SQLSTATE）常量，用于将驱动错误映射为统一领域错误。
const (
	// pgUniqueViolation 表示违反唯一约束（如名称重复）。
	pgUniqueViolation = "23505"
	// pgForeignKeyViolation 表示违反外键约束（如引用了不存在的父记录）。
	pgForeignKeyViolation = "23503"
)

// Repositories 聚合所有业务实体的仓储，便于在主程序装配时统一构造与注入。
//
// 各仓储均持有同一个 GORM 数据库句柄，底层连接由调用方在程序退出前关闭。
type Repositories struct {
	db *gorm.DB

	// Upstream 为上游 MCP 服务仓储。
	Upstream *UpstreamRepo
	// Alias 为别名规则仓储。
	Alias *AliasRepo
	// FilterMCP 为 MCP 级屏蔽规则仓储。
	FilterMCP *FilterMCPRepo
	// ToolPolicy 为工具策略规则仓储。
	ToolPolicy *ToolPolicyRepo
	// FilterAPIKey 为 API Key 级屏蔽规则仓储。
	FilterAPIKey *FilterAPIKeyRepo
	// APIKey 为 API Key 元数据仓储。
	APIKey *APIKeyRepo
	// ACL 为 API Key 来源白名单仓储。
	ACL *ACLRepo
	// APIKeyUpstreamAccess 为 API Key 上游访问范围仓储。
	APIKeyUpstreamAccess *APIKeyUpstreamAccessRepo
	// ToolCache 为工具缓存持久副本仓储。
	ToolCache *ToolCacheRepo
	// CallStat 为调用统计仓储。
	CallStat *CallStatRepo
	// Audit 为审计日志仓储。
	Audit *AuditRepo
	// Security 为安全事件与封禁记录仓储。
	Security *SecurityRepo
	// AIProvider 为 OpenAI-compatible 风险评级 Provider 仓储。
	AIProvider *AIProviderRepo
	// ToolRisk 为按来源工具身份保存的风险目录仓储。
	ToolRisk *ToolRiskRepo
	// RiskJob 为持久化评级任务仓储。
	RiskJob *RiskJobRepo
}

// NewRepositories 基于 GORM 数据库句柄构造所有仓储。
func NewRepositories(db *gorm.DB) *Repositories {
	return &Repositories{
		db:                   db,
		Upstream:             NewUpstreamRepo(db),
		Alias:                NewAliasRepo(db),
		FilterMCP:            NewFilterMCPRepo(db),
		ToolPolicy:           NewToolPolicyRepo(db),
		FilterAPIKey:         NewFilterAPIKeyRepo(db),
		APIKey:               NewAPIKeyRepo(db),
		ACL:                  NewACLRepo(db),
		APIKeyUpstreamAccess: NewAPIKeyUpstreamAccessRepo(db),
		ToolCache:            NewToolCacheRepo(db),
		CallStat:             NewCallStatRepo(db),
		Audit:                NewAuditRepo(db),
		Security:             NewSecurityRepo(db),
		AIProvider:           NewAIProviderRepo(db),
		ToolRisk:             NewToolRiskRepo(db),
		RiskJob:              NewRiskJobRepo(db),
	}
}

// WithTransaction 在同一个数据库事务内执行一组仓储操作。
//
// 回调收到的是绑定到事务句柄的新仓储集合；回调返回错误时整体回滚，返回 nil 时提交。
func (r *Repositories) WithTransaction(ctx context.Context, fn func(*Repositories) error) error {
	if r == nil || r.db == nil {
		return domain.NewError(domain.CodeValidation, "仓储集合未初始化")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(NewRepositories(tx))
	})
}

// newUUID 生成一条新记录的主键。
//
// 主键由应用侧生成，便于在持久化前即获得标识并返回给调用方，避免依赖数据库默认值。
//
// 用版本 7 而非版本 4：v7 的高 48 位是毫秒时间戳，生成的标识整体按时间递增，写入
// PostgreSQL 主键索引时接近顺序追加而非随机散布，可减少 B-tree 页分裂与写放大。
//
// 本函数的调用方都是管理动作驱动的低频配置表（上游、别名与屏蔽规则、工具策略、
// API Key 及其白名单、安全封禁），行数量级不大，索引收益有限；换用标准库实现的主要
// 目的是去掉自制的位操作与格式校验。高写入量的 call_stat 系列与 audit_log 走的是
// 复合主键与 bigserial，不经此处。
//
// 代价是标识本身透露了记录的创建时间。这些标识只在管理端可见（对外 MCP 只输出工具的
// name/description/inputSchema，不带任何主键），且创建时间本就是管理台展示的字段，
// 不构成新的信息暴露；API Key 的明文另由 crypto/rand 生成，与此处无关。
func newUUID() string {
	return uuid.NewV7().String()
}

// parseUUID 校验 UUID 字符串；空串视为 SQL NULL 场景，原样返回空串。
//
// 解析失败（格式非法）返回校验类错误，避免把非法标识透传到数据库层。
func parseUUID(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if !isUUID(s) {
		return "", domain.NewError(domain.CodeValidation, "标识格式非法："+s)
	}
	return s, nil
}

func nullableUUID(s string) (*string, error) {
	uid, err := parseUUID(s)
	if err != nil {
		return nil, err
	}
	if uid == "" {
		return nil, nil
	}
	return &uid, nil
}

// isUUID 判定字符串是否为标准 36 字符形式的 UUID（大小写不敏感）。
//
// 标准库 uuid.Parse 另外接受 "{...}" 与 "urn:uuid:..." 两种带修饰的写法，比这里需要的
// 宽松：它们解析出的标识与裸写法完全相同，一旦放行，同一条记录就会存在多种字符串表示，
// 从而绕过按字符串做的去重与等值判断（例如 uniqueValidUUIDs 的按串去重）。因此在解析
// 成功之外，额外要求输入本身就是规范形式。
func isUUID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id.String() == strings.ToLower(s)
}

// classifyWrite 将写操作（INSERT/UPDATE）的驱动错误映射为统一领域错误。
//   - 唯一约束冲突 → CONFLICT（如名称重复）。
//   - 外键约束冲突 → NOT_FOUND（引用的父记录不存在）。
//   - 其余错误原样返回。
func classifyWrite(err error, conflictMsg, notFoundMsg string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return domain.NewError(domain.CodeConflict, conflictMsg)
	}
	if errors.Is(err, gorm.ErrForeignKeyViolated) {
		return domain.NewError(domain.CodeNotFound, notFoundMsg)
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case pgUniqueViolation:
			return domain.NewError(domain.CodeConflict, conflictMsg)
		case pgForeignKeyViolation:
			return domain.NewError(domain.CodeNotFound, notFoundMsg)
		}
	}
	return err
}

// notFoundIfNoRows 将 GORM 未找到错误映射为 NOT_FOUND，其余错误原样返回。
func notFoundIfNoRows(err error, notFoundMsg string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.NewError(domain.CodeNotFound, notFoundMsg)
	}
	return err
}
