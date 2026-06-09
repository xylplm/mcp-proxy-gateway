package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// AliasRepo 提供别名规则（alias_rule 表）的类型安全增删查改。
type AliasRepo struct {
	pool *pgxpool.Pool
}

// NewAliasRepo 构造别名规则仓储。
func NewAliasRepo(pool *pgxpool.Pool) *AliasRepo {
	return &AliasRepo{pool: pool}
}

func (r *AliasRepo) Create(ctx context.Context, rule domain.AliasRule) (domain.AliasRule, error) {
	id := newUUID()
	rule.ScopeType = normalizeRuleScope(rule.ScopeType, rule.UpstreamIDs)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AliasRule{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const q = `
		INSERT INTO alias_rule
			(id, scope_type, pattern, is_regex, target_name, target_desc, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err = tx.Exec(ctx, q,
		id, rule.ScopeType, rule.Pattern, rule.IsRegex,
		nullableText(rule.TargetName), nullableText(rule.TargetDesc), rule.SortOrder,
	)
	if err != nil {
		return domain.AliasRule{}, classifyWrite(err, "别名规则冲突", "别名规则创建失败")
	}
	rule.ID = uuidString(id)
	if err := replaceAliasBindings(ctx, tx, rule.ID, rule.ScopeType, rule.UpstreamIDs); err != nil {
		return domain.AliasRule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AliasRule{}, err
	}
	return rule, nil
}

// Get 按标识查询单条别名规则；不存在返回 CodeNotFound。
func (r *AliasRepo) Get(ctx context.Context, id string) (domain.AliasRule, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return domain.AliasRule{}, err
	}
	const q = `
		SELECT id, scope_type, pattern, is_regex, target_name, target_desc, sort_order
		FROM alias_rule
		WHERE id = $1`
	rule, err := scanAlias(r.pool.QueryRow(ctx, q, uid))
	if err != nil {
		return domain.AliasRule{}, notFoundIfNoRows(err, "别名规则不存在")
	}
	rule.UpstreamIDs, err = r.listBindings(ctx, rule.ID)
	if err != nil {
		return domain.AliasRule{}, err
	}
	return rule, nil
}

// List 返回全部别名规则，按 sort_order 升序排列；无数据返回空切片。
func (r *AliasRepo) List(ctx context.Context) ([]domain.AliasRule, error) {
	const q = `
		SELECT id, scope_type, pattern, is_regex, target_name, target_desc, sort_order
		FROM alias_rule
		ORDER BY sort_order ASC, created_at ASC`
	rows, err := r.pool.Query(ctx, q)
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
		rule.UpstreamIDs, err = r.listBindings(ctx, rule.ID)
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

// ListByUpstream 返回适用于某个上游 MCP 的全部别名规则，按 sort_order 升序排列；无数据返回空切片（Req 8.5）。
func (r *AliasRepo) ListByUpstream(ctx context.Context, upstreamID string) ([]domain.AliasRule, error) {
	uid, err := parseUUID(upstreamID)
	if err != nil {
		return nil, err
	}
	const q = `
		SELECT ar.id, ar.scope_type, ar.pattern, ar.is_regex, ar.target_name, ar.target_desc, ar.sort_order
		FROM alias_rule ar
		LEFT JOIN alias_rule_upstream aru ON aru.rule_id = ar.id
		WHERE ar.scope_type = 'all' OR aru.upstream_id = $1
		ORDER BY ar.sort_order ASC, ar.created_at ASC`
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
		rule.UpstreamIDs, err = r.listBindings(ctx, rule.ID)
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

// Update 更新一条别名规则；不存在返回 CodeNotFound。
func (r *AliasRepo) Update(ctx context.Context, rule domain.AliasRule) (domain.AliasRule, error) {
	uid, err := parseUUID(rule.ID)
	if err != nil {
		return domain.AliasRule{}, err
	}
	rule.ScopeType = normalizeRuleScope(rule.ScopeType, rule.UpstreamIDs)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AliasRule{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const q = `
		UPDATE alias_rule
		SET scope_type = $2, pattern = $3, is_regex = $4, target_name = $5, target_desc = $6, sort_order = $7
		WHERE id = $1`
	tag, err := tx.Exec(ctx, q,
		uid, rule.ScopeType, rule.Pattern, rule.IsRegex,
		nullableText(rule.TargetName), nullableText(rule.TargetDesc), rule.SortOrder,
	)
	if err != nil {
		return domain.AliasRule{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.AliasRule{}, domain.NewError(domain.CodeNotFound, "别名规则不存在")
	}
	if err := replaceAliasBindings(ctx, tx, rule.ID, rule.ScopeType, rule.UpstreamIDs); err != nil {
		return domain.AliasRule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AliasRule{}, err
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
		scopeType  string
		pattern    string
		isRegex    bool
		targetName pgtype.Text
		targetDesc pgtype.Text
		sortOrder  int
	)
	if err := row.Scan(&id, &scopeType, &pattern, &isRegex, &targetName, &targetDesc, &sortOrder); err != nil {
		return domain.AliasRule{}, err
	}
	return domain.AliasRule{
		ID:         uuidString(id),
		ScopeType:  scopeType,
		Pattern:    pattern,
		IsRegex:    isRegex,
		TargetName: targetName.String,
		TargetDesc: targetDesc.String,
		SortOrder:  sortOrder,
	}, nil
}

func (r *AliasRepo) listBindings(ctx context.Context, ruleID string) ([]string, error) {
	uid, err := parseUUID(ruleID)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT upstream_id FROM alias_rule_upstream WHERE rule_id = $1 ORDER BY upstream_id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, uuidString(id))
	}
	return ids, rows.Err()
}

func (r *AliasRepo) replaceBindings(ctx context.Context, ruleID, scopeType string, upstreamIDs []string) error {
	return replaceAliasBindings(ctx, r.pool, ruleID, scopeType, upstreamIDs)
}

func replaceAliasBindings(ctx context.Context, exec interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, ruleID, scopeType string, upstreamIDs []string) error {
	ruleUID, err := parseUUID(ruleID)
	if err != nil {
		return err
	}
	if _, err := exec.Exec(ctx, `DELETE FROM alias_rule_upstream WHERE rule_id = $1`, ruleUID); err != nil {
		return err
	}
	if scopeType != "upstreams" {
		return nil
	}
	for _, upstreamID := range upstreamIDs {
		upUID, err := parseUUID(upstreamID)
		if err != nil {
			return err
		}
		_, err = exec.Exec(ctx, `INSERT INTO alias_rule_upstream (rule_id, upstream_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, ruleUID, upUID)
		if err != nil {
			return classifyWrite(err, "规则作用范围冲突", "选择的上游 MCP 不存在")
		}
	}
	return nil
}

func normalizeRuleScope(scopeType string, upstreamIDs []string) string {
	if scopeType == "upstreams" && len(upstreamIDs) > 0 {
		return "upstreams"
	}
	if scopeType == "" && len(upstreamIDs) > 0 {
		return "upstreams"
	}
	return "all"
}
