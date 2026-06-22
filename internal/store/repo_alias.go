package store

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// AliasRepo 提供别名规则（alias_rule 表）的类型安全增删查改。
type AliasRepo struct {
	db *gorm.DB
}

// NewAliasRepo 构造别名规则仓储。
func NewAliasRepo(db *gorm.DB) *AliasRepo {
	return &AliasRepo{db: db}
}

func (r *AliasRepo) Create(ctx context.Context, rule domain.AliasRule) (domain.AliasRule, error) {
	id := newUUID()
	rule.ScopeType = normalizeRuleScope(rule.ScopeType, rule.UpstreamIDs)
	model := aliasRuleModel{
		ID:         id,
		ScopeType:  rule.ScopeType,
		Pattern:    rule.Pattern,
		IsRegex:    rule.IsRegex,
		TargetName: nullableString(rule.TargetName),
		TargetDesc: nullableString(rule.TargetDesc),
		SortOrder:  rule.SortOrder,
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&aliasRuleModel{}).Create(map[string]any{
			"id":          model.ID,
			"scope_type":  model.ScopeType,
			"pattern":     model.Pattern,
			"is_regex":    model.IsRegex,
			"target_name": model.TargetName,
			"target_desc": model.TargetDesc,
			"sort_order":  model.SortOrder,
		}).Error; err != nil {
			return classifyWrite(err, "别名规则冲突", "别名规则创建失败")
		}
		return replaceAliasBindings(ctx, tx, id, rule.ScopeType, rule.UpstreamIDs)
	})
	if err != nil {
		return domain.AliasRule{}, err
	}
	rule.ID = id
	return rule, nil
}

// Get 按标识查询单条别名规则；不存在返回 CodeNotFound。
func (r *AliasRepo) Get(ctx context.Context, id string) (domain.AliasRule, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return domain.AliasRule{}, err
	}
	var model aliasRuleModel
	if err := r.db.WithContext(ctx).Where("id = ?", uid).First(&model).Error; err != nil {
		return domain.AliasRule{}, notFoundIfNoRows(err, "别名规则不存在")
	}
	rule := modelToAlias(model)
	rule.UpstreamIDs, err = r.listBindings(ctx, rule.ID)
	if err != nil {
		return domain.AliasRule{}, err
	}
	return rule, nil
}

// List 返回全部别名规则，按 sort_order 升序排列；无数据返回空切片。
func (r *AliasRepo) List(ctx context.Context) ([]domain.AliasRule, error) {
	var models []aliasRuleModel
	if err := r.db.WithContext(ctx).Order("sort_order ASC").Order("created_at ASC").Find(&models).Error; err != nil {
		return nil, err
	}
	return r.aliasModelsToRules(ctx, models)
}

// ListByUpstream 返回适用于某个上游 MCP 的全部别名规则，按 sort_order 升序排列；无数据返回空切片（Req 8.5）。
func (r *AliasRepo) ListByUpstream(ctx context.Context, upstreamID string) ([]domain.AliasRule, error) {
	uid, err := parseUUID(upstreamID)
	if err != nil {
		return nil, err
	}
	var models []aliasRuleModel
	err = r.db.WithContext(ctx).Raw(`
		SELECT ar.id, ar.scope_type, ar.pattern, ar.is_regex, ar.target_name, ar.target_desc, ar.sort_order, ar.created_at
		FROM alias_rule ar
		LEFT JOIN alias_rule_upstream aru ON aru.rule_id = ar.id
		WHERE ar.scope_type = 'all' OR aru.upstream_id = ?
		ORDER BY ar.sort_order ASC, ar.created_at ASC`, uid).Scan(&models).Error
	if err != nil {
		return nil, err
	}
	return r.aliasModelsToRules(ctx, models)
}

// Update 更新一条别名规则；不存在返回 CodeNotFound。
func (r *AliasRepo) Update(ctx context.Context, rule domain.AliasRule) (domain.AliasRule, error) {
	uid, err := parseUUID(rule.ID)
	if err != nil {
		return domain.AliasRule{}, err
	}
	rule.ScopeType = normalizeRuleScope(rule.ScopeType, rule.UpstreamIDs)
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&aliasRuleModel{}).Where("id = ?", uid).Updates(map[string]any{
			"scope_type":  rule.ScopeType,
			"pattern":     rule.Pattern,
			"is_regex":    rule.IsRegex,
			"target_name": nullableString(rule.TargetName),
			"target_desc": nullableString(rule.TargetDesc),
			"sort_order":  rule.SortOrder,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return domain.NewError(domain.CodeNotFound, "别名规则不存在")
		}
		return replaceAliasBindings(ctx, tx, rule.ID, rule.ScopeType, rule.UpstreamIDs)
	})
	if err != nil {
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
	res := r.db.WithContext(ctx).Where("id = ?", uid).Delete(&aliasRuleModel{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.NewError(domain.CodeNotFound, "别名规则不存在")
	}
	return nil
}

func modelToAlias(model aliasRuleModel) domain.AliasRule {
	return domain.AliasRule{
		ID:         model.ID,
		ScopeType:  model.ScopeType,
		Pattern:    model.Pattern,
		IsRegex:    model.IsRegex,
		TargetName: stringValue(model.TargetName),
		TargetDesc: stringValue(model.TargetDesc),
		SortOrder:  model.SortOrder,
	}
}

func (r *AliasRepo) aliasModelsToRules(ctx context.Context, models []aliasRuleModel) ([]domain.AliasRule, error) {
	result := make([]domain.AliasRule, 0, len(models))
	for _, model := range models {
		rule := modelToAlias(model)
		ids, err := r.listBindings(ctx, rule.ID)
		if err != nil {
			return nil, err
		}
		rule.UpstreamIDs = ids
		result = append(result, rule)
	}
	return result, nil
}

func (r *AliasRepo) listBindings(ctx context.Context, ruleID string) ([]string, error) {
	uid, err := parseUUID(ruleID)
	if err != nil {
		return nil, err
	}
	var bindings []aliasRuleUpstreamModel
	if err := r.db.WithContext(ctx).Where("rule_id = ?", uid).Order("upstream_id ASC").Find(&bindings).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		ids = append(ids, binding.UpstreamID)
	}
	return ids, nil
}

func (r *AliasRepo) replaceBindings(ctx context.Context, ruleID, scopeType string, upstreamIDs []string) error {
	return replaceAliasBindings(ctx, r.db, ruleID, scopeType, upstreamIDs)
}

func replaceAliasBindings(ctx context.Context, db *gorm.DB, ruleID, scopeType string, upstreamIDs []string) error {
	ruleUID, err := parseUUID(ruleID)
	if err != nil {
		return err
	}
	if err := db.WithContext(ctx).Where("rule_id = ?", ruleUID).Delete(&aliasRuleUpstreamModel{}).Error; err != nil {
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
		binding := aliasRuleUpstreamModel{RuleID: ruleUID, UpstreamID: upUID}
		err = db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error
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
