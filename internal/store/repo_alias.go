package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// AliasRepo 提供别名规则（alias_rule 表）的类型安全增删查改。
//
// 别名规则绑定在某个上游 MCP 上（upstream_id），通过外键 ON DELETE CASCADE
// 随上游删除而级联清理。domain.AliasRule 自身不含 created_at 字段，
// 仓储仅在持久层维护该时间戳，不向领域类型透出。
type AliasRepo struct {
	pool *pgxpool.Pool
}

// NewAliasRepo 构造别名规则仓储。
func NewAliasRepo(pool *pgxpool.Pool) *AliasRepo {
	return &AliasRepo{pool: pool}
}

// Create 持久化一条别名规则并回填生成标识（Req 8.1）。
//   - 绑定的 upstream_id 不存在（违反外键）返回 CodeNotFound。
func (r *AliasRepo) Create(ctx context.Context, rule domain.AliasRule) (domain.AliasRule, error) {
	upstreamID, err := parseUUID(rule.UpstreamID)
	if err != nil {
		return domain.AliasRule{}, err
	}
	id := newUUID()
	const q = `
		INSERT INTO alias_rule
			(id, upstream_id, pattern, is_regex, target_name, target_desc, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err = r.pool.Exec(ctx, q,
		id, upstreamID, rule.Pattern, rule.IsRegex,
		nullableText(rule.TargetName), nullableText(rule.TargetDesc), rule.SortOrder,
	)
	if err != nil {
		return domain.AliasRule{}, classifyWrite(err, "别名规则冲突", "绑定的上游 MCP 不存在")
	}
	rule.ID = uuidString(id)
	return rule, nil
}

// Get 按标识查询单条别名规则；不存在返回 CodeNotFound。
func (r *AliasRepo) Get(ctx context.Context, id string) (domain.AliasRule, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return domain.AliasRule{}, err
	}
	const q = `
		SELECT id, upstream_id, pattern, is_regex, target_name, target_desc, sort_order
		FROM alias_rule
		WHERE id = $1`
	rule, err := scanAlias(r.pool.QueryRow(ctx, q, uid))
	if err != nil {
		return domain.AliasRule{}, notFoundIfNoRows(err, "别名规则不存在")
	}
	return rule, nil
}

// ListByUpstream 返回某个上游 MCP 的全部别名规则，按 sort_order 升序排列；无数据返回空切片（Req 8.5）。
func (r *AliasRepo) ListByUpstream(ctx context.Context, upstreamID string) ([]domain.AliasRule, error) {
	uid, err := parseUUID(upstreamID)
	if err != nil {
		return nil, err
	}
	const q = `
		SELECT id, upstream_id, pattern, is_regex, target_name, target_desc, sort_order
		FROM alias_rule
		WHERE upstream_id = $1
		ORDER BY sort_order ASC, created_at ASC`
	rows, err := r.pool.Query(ctx, q, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]domain.AliasRule, 0)
	for rows.Next() {
		rule, err := scanAlias(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// Update 更新一条别名规则（不允许变更绑定的 upstream_id）；不存在返回 CodeNotFound。
func (r *AliasRepo) Update(ctx context.Context, rule domain.AliasRule) (domain.AliasRule, error) {
	uid, err := parseUUID(rule.ID)
	if err != nil {
		return domain.AliasRule{}, err
	}
	const q = `
		UPDATE alias_rule
		SET pattern = $2, is_regex = $3, target_name = $4, target_desc = $5, sort_order = $6
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q,
		uid, rule.Pattern, rule.IsRegex,
		nullableText(rule.TargetName), nullableText(rule.TargetDesc), rule.SortOrder,
	)
	if err != nil {
		return domain.AliasRule{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.AliasRule{}, domain.NewError(domain.CodeNotFound, "别名规则不存在")
	}
	return rule, nil
}

// Delete 删除一条别名规则；不存在返回 CodeNotFound。
func (r *AliasRepo) Delete(ctx context.Context, id string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	const q = `DELETE FROM alias_rule WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, uid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NewError(domain.CodeNotFound, "别名规则不存在")
	}
	return nil
}

// scanAlias 从单行结果扫描出 domain.AliasRule。
func scanAlias(row pgx.Row) (domain.AliasRule, error) {
	var (
		id         pgtype.UUID
		upstreamID pgtype.UUID
		pattern    string
		isRegex    bool
		targetName pgtype.Text
		targetDesc pgtype.Text
		sortOrder  int
	)
	if err := row.Scan(&id, &upstreamID, &pattern, &isRegex, &targetName, &targetDesc, &sortOrder); err != nil {
		return domain.AliasRule{}, err
	}
	return domain.AliasRule{
		ID:         uuidString(id),
		UpstreamID: uuidString(upstreamID),
		Pattern:    pattern,
		IsRegex:    isRegex,
		TargetName: targetName.String,
		TargetDesc: targetDesc.String,
		SortOrder:  sortOrder,
	}, nil
}
