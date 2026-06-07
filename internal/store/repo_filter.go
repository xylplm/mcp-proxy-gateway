package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// FilterMCPRow 是 MCP 级屏蔽规则（filter_rule_mcp 表）的行表示。
//
// 在 domain.FilterRule 基础上额外携带其绑定的上游 MCP 标识 UpstreamID。
type FilterMCPRow struct {
	domain.FilterRule
	// UpstreamID 为该屏蔽规则绑定的上游 MCP 标识。
	UpstreamID string
}

// FilterMCPRepo 提供 MCP 级屏蔽规则的类型安全增删查改与计数。
type FilterMCPRepo struct {
	pool *pgxpool.Pool
}

// NewFilterMCPRepo 构造 MCP 级屏蔽规则仓储。
func NewFilterMCPRepo(pool *pgxpool.Pool) *FilterMCPRepo {
	return &FilterMCPRepo{pool: pool}
}

// Create 持久化一条 MCP 级屏蔽规则并回填生成标识（Req 9.1）。
//   - 绑定的 upstream_id 不存在（违反外键）返回 CodeNotFound。
//
// 数量上限（100 条，Req 9.2/9.9）由应用层借助 CountByUpstream 校验，不在此处强制。
func (r *FilterMCPRepo) Create(ctx context.Context, row FilterMCPRow) (FilterMCPRow, error) {
	upstreamID, err := parseUUID(row.UpstreamID)
	if err != nil {
		return FilterMCPRow{}, err
	}
	id := newUUID()
	const q = `
		INSERT INTO filter_rule_mcp
			(id, upstream_id, pattern, is_regex, enabled, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err = r.pool.Exec(ctx, q,
		id, upstreamID, row.Pattern, row.IsRegex, row.Enabled, row.SortOrder,
	)
	if err != nil {
		return FilterMCPRow{}, classifyWrite(err, "屏蔽规则冲突", "绑定的上游 MCP 不存在")
	}
	row.ID = uuidString(id)
	return row, nil
}

// Get 按标识查询单条 MCP 级屏蔽规则；不存在返回 CodeNotFound。
func (r *FilterMCPRepo) Get(ctx context.Context, id string) (FilterMCPRow, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return FilterMCPRow{}, err
	}
	const q = `
		SELECT id, upstream_id, pattern, is_regex, enabled, sort_order
		FROM filter_rule_mcp
		WHERE id = $1`
	row, err := scanFilterMCP(r.pool.QueryRow(ctx, q, uid))
	if err != nil {
		return FilterMCPRow{}, notFoundIfNoRows(err, "屏蔽规则不存在")
	}
	return row, nil
}

// ListByUpstream 返回某上游 MCP 的全部屏蔽规则，按 sort_order 升序；无数据返回空切片。
func (r *FilterMCPRepo) ListByUpstream(ctx context.Context, upstreamID string) ([]FilterMCPRow, error) {
	uid, err := parseUUID(upstreamID)
	if err != nil {
		return nil, err
	}
	const q = `
		SELECT id, upstream_id, pattern, is_regex, enabled, sort_order
		FROM filter_rule_mcp
		WHERE upstream_id = $1
		ORDER BY sort_order ASC, created_at ASC`
	rows, err := r.pool.Query(ctx, q, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]FilterMCPRow, 0)
	for rows.Next() {
		row, err := scanFilterMCP(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// CountByUpstream 统计某上游 MCP 已有的屏蔽规则数量，供应用层做上限校验（Req 9.2/9.9）。
func (r *FilterMCPRepo) CountByUpstream(ctx context.Context, upstreamID string) (int, error) {
	uid, err := parseUUID(upstreamID)
	if err != nil {
		return 0, err
	}
	const q = `SELECT count(*) FROM filter_rule_mcp WHERE upstream_id = $1`
	var n int
	if err := r.pool.QueryRow(ctx, q, uid).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// Update 更新一条 MCP 级屏蔽规则（不变更绑定的 upstream_id）；不存在返回 CodeNotFound。
func (r *FilterMCPRepo) Update(ctx context.Context, row FilterMCPRow) (FilterMCPRow, error) {
	uid, err := parseUUID(row.ID)
	if err != nil {
		return FilterMCPRow{}, err
	}
	const q = `
		UPDATE filter_rule_mcp
		SET pattern = $2, is_regex = $3, enabled = $4, sort_order = $5
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, uid, row.Pattern, row.IsRegex, row.Enabled, row.SortOrder)
	if err != nil {
		return FilterMCPRow{}, err
	}
	if tag.RowsAffected() == 0 {
		return FilterMCPRow{}, domain.NewError(domain.CodeNotFound, "屏蔽规则不存在")
	}
	return row, nil
}

// SetEnabled 仅更新某条 MCP 级屏蔽规则的启停状态（Req 9.11）；不存在返回 CodeNotFound。
func (r *FilterMCPRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	const q = `UPDATE filter_rule_mcp SET enabled = $2 WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, uid, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NewError(domain.CodeNotFound, "屏蔽规则不存在")
	}
	return nil
}

// Delete 删除一条 MCP 级屏蔽规则；不存在返回 CodeNotFound。
func (r *FilterMCPRepo) Delete(ctx context.Context, id string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	const q = `DELETE FROM filter_rule_mcp WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, uid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NewError(domain.CodeNotFound, "屏蔽规则不存在")
	}
	return nil
}

// scanFilterMCP 从单行结果扫描出 FilterMCPRow。
func scanFilterMCP(row pgx.Row) (FilterMCPRow, error) {
	var (
		id         pgtype.UUID
		upstreamID pgtype.UUID
		pattern    string
		isRegex    bool
		enabled    bool
		sortOrder  int
	)
	if err := row.Scan(&id, &upstreamID, &pattern, &isRegex, &enabled, &sortOrder); err != nil {
		return FilterMCPRow{}, err
	}
	out := FilterMCPRow{UpstreamID: uuidString(upstreamID)}
	out.ID = uuidString(id)
	out.Pattern = pattern
	out.IsRegex = isRegex
	out.Enabled = enabled
	out.SortOrder = sortOrder
	return out, nil
}

// FilterAPIKeyRow 是 API Key 级屏蔽规则（filter_rule_apikey 表）的行表示。
//
// 在 domain.FilterRule 基础上额外携带其绑定的 API Key 标识 APIKeyID。
type FilterAPIKeyRow struct {
	domain.FilterRule
	// APIKeyID 为该屏蔽规则绑定的 API Key 标识。
	APIKeyID string
}

// FilterAPIKeyRepo 提供 API Key 级屏蔽规则的类型安全增删查改与计数。
type FilterAPIKeyRepo struct {
	pool *pgxpool.Pool
}

// NewFilterAPIKeyRepo 构造 API Key 级屏蔽规则仓储。
func NewFilterAPIKeyRepo(pool *pgxpool.Pool) *FilterAPIKeyRepo {
	return &FilterAPIKeyRepo{pool: pool}
}

// Create 持久化一条 API Key 级屏蔽规则并回填生成标识（Req 13.1）。
//   - 绑定的 api_key_id 不存在（违反外键）返回 CodeNotFound。
//
// 数量上限（100 条，Req 13.2/13.3）由应用层借助 CountByAPIKey 校验，不在此处强制。
func (r *FilterAPIKeyRepo) Create(ctx context.Context, row FilterAPIKeyRow) (FilterAPIKeyRow, error) {
	apiKeyID, err := parseUUID(row.APIKeyID)
	if err != nil {
		return FilterAPIKeyRow{}, err
	}
	id := newUUID()
	const q = `
		INSERT INTO filter_rule_apikey
			(id, api_key_id, pattern, is_regex, enabled, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err = r.pool.Exec(ctx, q,
		id, apiKeyID, row.Pattern, row.IsRegex, row.Enabled, row.SortOrder,
	)
	if err != nil {
		return FilterAPIKeyRow{}, classifyWrite(err, "API Key 屏蔽规则冲突", "绑定的 API Key 不存在")
	}
	row.ID = uuidString(id)
	return row, nil
}

// Get 按标识查询单条 API Key 级屏蔽规则；不存在返回 CodeNotFound。
func (r *FilterAPIKeyRepo) Get(ctx context.Context, id string) (FilterAPIKeyRow, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return FilterAPIKeyRow{}, err
	}
	const q = `
		SELECT id, api_key_id, pattern, is_regex, enabled, sort_order
		FROM filter_rule_apikey
		WHERE id = $1`
	row, err := scanFilterAPIKey(r.pool.QueryRow(ctx, q, uid))
	if err != nil {
		return FilterAPIKeyRow{}, notFoundIfNoRows(err, "API Key 屏蔽规则不存在")
	}
	return row, nil
}

// ListByAPIKey 返回某 API Key 的全部屏蔽规则，按 sort_order 升序；无数据返回空切片。
func (r *FilterAPIKeyRepo) ListByAPIKey(ctx context.Context, apiKeyID string) ([]FilterAPIKeyRow, error) {
	uid, err := parseUUID(apiKeyID)
	if err != nil {
		return nil, err
	}
	const q = `
		SELECT id, api_key_id, pattern, is_regex, enabled, sort_order
		FROM filter_rule_apikey
		WHERE api_key_id = $1
		ORDER BY sort_order ASC`
	rows, err := r.pool.Query(ctx, q, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]FilterAPIKeyRow, 0)
	for rows.Next() {
		row, err := scanFilterAPIKey(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// CountByAPIKey 统计某 API Key 已有的屏蔽规则数量，供应用层做上限校验（Req 13.2/13.3）。
func (r *FilterAPIKeyRepo) CountByAPIKey(ctx context.Context, apiKeyID string) (int, error) {
	uid, err := parseUUID(apiKeyID)
	if err != nil {
		return 0, err
	}
	const q = `SELECT count(*) FROM filter_rule_apikey WHERE api_key_id = $1`
	var n int
	if err := r.pool.QueryRow(ctx, q, uid).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// Update 更新一条 API Key 级屏蔽规则（不变更绑定的 api_key_id）；不存在返回 CodeNotFound。
func (r *FilterAPIKeyRepo) Update(ctx context.Context, row FilterAPIKeyRow) (FilterAPIKeyRow, error) {
	uid, err := parseUUID(row.ID)
	if err != nil {
		return FilterAPIKeyRow{}, err
	}
	const q = `
		UPDATE filter_rule_apikey
		SET pattern = $2, is_regex = $3, enabled = $4, sort_order = $5
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, uid, row.Pattern, row.IsRegex, row.Enabled, row.SortOrder)
	if err != nil {
		return FilterAPIKeyRow{}, err
	}
	if tag.RowsAffected() == 0 {
		return FilterAPIKeyRow{}, domain.NewError(domain.CodeNotFound, "API Key 屏蔽规则不存在")
	}
	return row, nil
}

// SetEnabled 仅更新某条 API Key 级屏蔽规则的启停状态（Req 13.8）；不存在返回 CodeNotFound。
func (r *FilterAPIKeyRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	const q = `UPDATE filter_rule_apikey SET enabled = $2 WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, uid, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NewError(domain.CodeNotFound, "API Key 屏蔽规则不存在")
	}
	return nil
}

// Delete 删除一条 API Key 级屏蔽规则；不存在返回 CodeNotFound。
func (r *FilterAPIKeyRepo) Delete(ctx context.Context, id string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	const q = `DELETE FROM filter_rule_apikey WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, uid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NewError(domain.CodeNotFound, "API Key 屏蔽规则不存在")
	}
	return nil
}

// scanFilterAPIKey 从单行结果扫描出 FilterAPIKeyRow。
func scanFilterAPIKey(row pgx.Row) (FilterAPIKeyRow, error) {
	var (
		id        pgtype.UUID
		apiKeyID  pgtype.UUID
		pattern   string
		isRegex   bool
		enabled   bool
		sortOrder int
	)
	if err := row.Scan(&id, &apiKeyID, &pattern, &isRegex, &enabled, &sortOrder); err != nil {
		return FilterAPIKeyRow{}, err
	}
	out := FilterAPIKeyRow{APIKeyID: uuidString(apiKeyID)}
	out.ID = uuidString(id)
	out.Pattern = pattern
	out.IsRegex = isRegex
	out.Enabled = enabled
	out.SortOrder = sortOrder
	return out, nil
}
