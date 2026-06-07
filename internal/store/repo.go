package store

import (
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

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
// 各仓储均持有同一个 pgxpool.Pool，连接由调用方在程序退出前关闭。
type Repositories struct {
	// Upstream 为上游 MCP 服务仓储。
	Upstream *UpstreamRepo
	// Alias 为别名规则仓储。
	Alias *AliasRepo
	// FilterMCP 为 MCP 级屏蔽规则仓储。
	FilterMCP *FilterMCPRepo
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
}

// NewRepositories 基于连接池构造所有仓储。
func NewRepositories(pool *pgxpool.Pool) *Repositories {
	return &Repositories{
		Upstream:     NewUpstreamRepo(pool),
		Alias:        NewAliasRepo(pool),
		FilterMCP:    NewFilterMCPRepo(pool),
		FilterAPIKey: NewFilterAPIKeyRepo(pool),
		APIKey:       NewAPIKeyRepo(pool),
		ACL:          NewACLRepo(pool),
		ToolCache:    NewToolCacheRepo(pool),
		CallStat:     NewCallStatRepo(pool),
		Audit:        NewAuditRepo(pool),
	}
}

// newUUID 生成一个版本 4 的随机 UUID，返回可直接用于 pgx 参数的 pgtype.UUID。
//
// 主键由应用侧生成，便于在持久化前即获得标识并返回给调用方，避免依赖数据库默认值。
func newUUID() pgtype.UUID {
	var b [16]byte
	// crypto/rand.Read 在常规平台上不会失败；即便失败也只会得到全零字节，
	// 仍是合法的 UUID 值，不影响主键唯一性以外的正确性。
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // 版本号 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10xx
	return pgtype.UUID{Bytes: b, Valid: true}
}

// parseUUID 将字符串解析为 pgtype.UUID；空串视为 SQL NULL（Valid=false）。
//
// 解析失败（格式非法）返回校验类错误，避免把非法标识透传到数据库层。
func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if s == "" {
		return u, nil
	}
	if err := u.Scan(s); err != nil {
		return u, domain.NewError(domain.CodeValidation, "标识格式非法："+err.Error())
	}
	return u, nil
}

// uuidString 将 pgtype.UUID 转为标准 8-4-4-4-12 字符串；NULL 返回空串。
func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// nullableText 将字符串转为可空文本参数：空串编码为 SQL NULL。
func nullableText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

// nullableInt 将 *int 转为可空整型参数：nil 编码为 SQL NULL。
func nullableInt(p *int) pgtype.Int4 {
	if p == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*p), Valid: true}
}

// intPtr 将可空整型读取结果转为 *int：NULL 返回 nil。
func intPtr(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int32)
	return &n
}

// nullableTime 将 *time.Time 转为可空时间戳参数：nil 编码为 SQL NULL。
func nullableTime(p *time.Time) pgtype.Timestamptz {
	if p == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *p, Valid: true}
}

// timePtr 将可空时间戳读取结果转为 *time.Time：NULL 返回 nil。
func timePtr(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time
	return &t
}

// classifyWrite 将写操作（INSERT/UPDATE）的驱动错误映射为统一领域错误。
//   - 唯一约束冲突 → CONFLICT（如名称重复）。
//   - 外键约束冲突 → NOT_FOUND（引用的父记录不存在）。
//   - 其余错误原样返回。
func classifyWrite(err error, conflictMsg, notFoundMsg string) error {
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

// notFoundIfNoRows 将 pgx.ErrNoRows 映射为 NOT_FOUND，其余错误原样返回。
func notFoundIfNoRows(err error, notFoundMsg string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NewError(domain.CodeNotFound, notFoundMsg)
	}
	return err
}
