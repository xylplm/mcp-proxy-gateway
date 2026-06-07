package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// ToolCacheRepo 提供工具缓存持久副本（tool_cache 表）的存取（Req 6.1）。
//
// Redis 为热路径、PG 为持久兜底。该仓储以「整列表替换」为唯一写语义，
// 与 domain.Tool_Cache 的 Replace 契约一致，避免增量合并带来的不一致。
type ToolCacheRepo struct {
	pool *pgxpool.Pool
}

// NewToolCacheRepo 构造工具缓存持久副本仓储。
func NewToolCacheRepo(pool *pgxpool.Pool) *ToolCacheRepo {
	return &ToolCacheRepo{pool: pool}
}

// Replace 以整列表替换语义写入某上游 MCP 的工具列表与更新时间（Req 6.1）。
//
// tool_cache.upstream_id 为主键，故采用 UPSERT；nil 列表序列化为空 JSON 数组。
//   - 绑定的 upstream_id 不存在（违反外键）返回 CodeNotFound。
func (r *ToolCacheRepo) Replace(ctx context.Context, upstreamID string, tools []domain.ToolDef, updatedAt time.Time) error {
	uid, err := parseUUID(upstreamID)
	if err != nil {
		return err
	}
	if tools == nil {
		tools = []domain.ToolDef{}
	}
	payload, err := json.Marshal(tools)
	if err != nil {
		return domain.NewError(domain.CodeValidation, "工具列表序列化失败："+err.Error())
	}

	const q = `
		INSERT INTO tool_cache (upstream_id, tools, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (upstream_id)
		DO UPDATE SET tools = EXCLUDED.tools, updated_at = EXCLUDED.updated_at`
	if _, err := r.pool.Exec(ctx, q, uid, payload, updatedAt); err != nil {
		return classifyWrite(err, "工具缓存冲突", "绑定的上游 MCP 不存在")
	}
	return nil
}

// Get 读取某上游 MCP 持久化的工具列表及其更新时间。
//
// 返回值 found 为 false 表示该上游尚无持久缓存（缺失而非错误，Req 6.3）。
func (r *ToolCacheRepo) Get(ctx context.Context, upstreamID string) (tools []domain.ToolDef, updatedAt time.Time, found bool, err error) {
	uid, perr := parseUUID(upstreamID)
	if perr != nil {
		return nil, time.Time{}, false, perr
	}
	const q = `SELECT tools, updated_at FROM tool_cache WHERE upstream_id = $1`
	var (
		payload []byte
		ts      time.Time
	)
	scanErr := r.pool.QueryRow(ctx, q, uid).Scan(&payload, &ts)
	if scanErr != nil {
		// 缓存缺失返回 found=false，不视为错误。
		if e := notFoundIfNoRows(scanErr, "工具缓存不存在"); e != scanErr {
			return nil, time.Time{}, false, nil
		}
		return nil, time.Time{}, false, scanErr
	}

	out := make([]domain.ToolDef, 0)
	if len(payload) > 0 {
		if e := json.Unmarshal(payload, &out); e != nil {
			return nil, time.Time{}, false, domain.NewError(domain.CodeValidation, "工具列表反序列化失败："+e.Error())
		}
	}
	return out, ts, true, nil
}

// Delete 删除某上游 MCP 的持久缓存（Req 6.6）。
//
// 删除不存在的记录不视为错误（删除语义幂等），返回是否实际删除。
func (r *ToolCacheRepo) Delete(ctx context.Context, upstreamID string) (bool, error) {
	uid, err := parseUUID(upstreamID)
	if err != nil {
		return false, err
	}
	const q = `DELETE FROM tool_cache WHERE upstream_id = $1`
	tag, err := r.pool.Exec(ctx, q, uid)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
