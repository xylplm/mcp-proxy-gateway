package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

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
	// ToolCache 为工具缓存持久副本仓储。
	ToolCache *ToolCacheRepo
	// CallStat 为调用统计仓储。
	CallStat *CallStatRepo
	// Audit 为审计日志仓储。
	Audit *AuditRepo
	// Security 为安全事件与封禁记录仓储。
	Security *SecurityRepo
}

// NewRepositories 基于 GORM 数据库句柄构造所有仓储。
func NewRepositories(db *gorm.DB) *Repositories {
	return &Repositories{
		db:           db,
		Upstream:     NewUpstreamRepo(db),
		Alias:        NewAliasRepo(db),
		FilterMCP:    NewFilterMCPRepo(db),
		ToolPolicy:   NewToolPolicyRepo(db),
		FilterAPIKey: NewFilterAPIKeyRepo(db),
		APIKey:       NewAPIKeyRepo(db),
		ACL:          NewACLRepo(db),
		ToolCache:    NewToolCacheRepo(db),
		CallStat:     NewCallStatRepo(db),
		Audit:        NewAuditRepo(db),
		Security:     NewSecurityRepo(db),
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

// newUUID 生成一个版本 4 的随机 UUID 字符串。
//
// 主键由应用侧生成，便于在持久化前即获得标识并返回给调用方，避免依赖数据库默认值。
func newUUID() string {
	var b [16]byte
	// crypto/rand.Read 在常规平台上不会失败；即便失败也只会得到全零字节，
	// 仍是合法的 UUID 值，不影响主键唯一性以外的正确性。
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // 版本号 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10xx
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
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

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, ch := range s {
		switch i {
		case 8, 13, 18, 23:
			if ch != '-' {
				return false
			}
		default:
			if !isHex(ch) {
				return false
			}
		}
	}
	return true
}

func isHex(ch rune) bool {
	return ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F'
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
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
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
