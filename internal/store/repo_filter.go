package store

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// FilterMCPRow 是 MCP 级屏蔽规则（filter_rule_mcp 表）的行表示。
type FilterMCPRow struct {
	domain.FilterRule
}

// FilterMCPRepo 提供 MCP 级屏蔽规则的类型安全增删查改与计数。
type FilterMCPRepo struct {
	db *gorm.DB
}

// NewFilterMCPRepo 构造 MCP 级屏蔽规则仓储。
func NewFilterMCPRepo(db *gorm.DB) *FilterMCPRepo {
	return &FilterMCPRepo{db: db}
}

func (r *FilterMCPRepo) Create(ctx context.Context, row FilterMCPRow) (FilterMCPRow, error) {
	id := newUUID()
	row.ScopeType = normalizeRuleScope(row.ScopeType, row.UpstreamIDs)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&filterRuleMCPModel{}).Create(map[string]any{
			"id":         id,
			"scope_type": row.ScopeType,
			"pattern":    row.Pattern,
			"is_regex":   row.IsRegex,
			"enabled":    row.Enabled,
			"sort_order": row.SortOrder,
		}).Error; err != nil {
			return classifyWrite(err, "屏蔽规则冲突", "屏蔽规则创建失败")
		}
		return replaceFilterMCPBindings(ctx, tx, id, row.ScopeType, row.UpstreamIDs)
	})
	if err != nil {
		return FilterMCPRow{}, err
	}
	row.ID = id
	return row, nil
}

// Get 按标识查询单条 MCP 级屏蔽规则；不存在返回 CodeNotFound。
func (r *FilterMCPRepo) Get(ctx context.Context, id string) (FilterMCPRow, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return FilterMCPRow{}, err
	}
	var model filterRuleMCPModel
	if err := r.db.WithContext(ctx).Where("id = ?", uid).First(&model).Error; err != nil {
		return FilterMCPRow{}, notFoundIfNoRows(err, "屏蔽规则不存在")
	}
	row := modelToFilterMCP(model)
	row.UpstreamIDs, err = r.listBindings(ctx, row.ID)
	if err != nil {
		return FilterMCPRow{}, err
	}
	return row, nil
}

// List 返回全部 MCP 级屏蔽规则，按 sort_order 升序；无数据返回空切片。
func (r *FilterMCPRepo) List(ctx context.Context) ([]FilterMCPRow, error) {
	var models []filterRuleMCPModel
	if err := r.db.WithContext(ctx).Order("sort_order ASC").Order("created_at ASC").Find(&models).Error; err != nil {
		return nil, err
	}
	return r.filterMCPModelsToRows(ctx, models)
}

// ListByUpstream 返回适用于某上游 MCP 的全部屏蔽规则，按 sort_order 升序；无数据返回空切片。
func (r *FilterMCPRepo) ListByUpstream(ctx context.Context, upstreamID string) ([]FilterMCPRow, error) {
	uid, err := parseUUID(upstreamID)
	if err != nil {
		return nil, err
	}
	var models []filterRuleMCPModel
	err = r.db.WithContext(ctx).Raw(`
		SELECT fr.id, fr.scope_type, fr.pattern, fr.is_regex, fr.enabled, fr.sort_order, fr.created_at
		FROM filter_rule_mcp fr
		LEFT JOIN filter_rule_mcp_upstream fru ON fru.rule_id = fr.id
		WHERE fr.scope_type = 'all' OR fru.upstream_id = ?
		ORDER BY fr.sort_order ASC, fr.created_at ASC`, uid).Scan(&models).Error
	if err != nil {
		return nil, err
	}
	return r.filterMCPModelsToRows(ctx, models)
}

// Count 统计全部 MCP 级屏蔽规则数量，供应用层做上限校验。
func (r *FilterMCPRepo) Count(ctx context.Context) (int, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&filterRuleMCPModel{}).Count(&n).Error; err != nil {
		return 0, err
	}
	return int(n), nil
}

// Update 更新一条 MCP 级屏蔽规则；不存在返回 CodeNotFound。
func (r *FilterMCPRepo) Update(ctx context.Context, row FilterMCPRow) (FilterMCPRow, error) {
	uid, err := parseUUID(row.ID)
	if err != nil {
		return FilterMCPRow{}, err
	}
	row.ScopeType = normalizeRuleScope(row.ScopeType, row.UpstreamIDs)
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&filterRuleMCPModel{}).Where("id = ?", uid).Updates(map[string]any{
			"scope_type": row.ScopeType,
			"pattern":    row.Pattern,
			"is_regex":   row.IsRegex,
			"enabled":    row.Enabled,
			"sort_order": row.SortOrder,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return domain.NewError(domain.CodeNotFound, "屏蔽规则不存在")
		}
		return replaceFilterMCPBindings(ctx, tx, row.ID, row.ScopeType, row.UpstreamIDs)
	})
	if err != nil {
		return FilterMCPRow{}, err
	}
	return row, nil
}

// SetEnabled 仅更新某条 MCP 级屏蔽规则的启停状态（Req 9.11）；不存在返回 CodeNotFound。
func (r *FilterMCPRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	res := r.db.WithContext(ctx).Model(&filterRuleMCPModel{}).Where("id = ?", uid).Update("enabled", enabled)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
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
	res := r.db.WithContext(ctx).Where("id = ?", uid).Delete(&filterRuleMCPModel{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.NewError(domain.CodeNotFound, "屏蔽规则不存在")
	}
	return nil
}

func modelToFilterMCP(model filterRuleMCPModel) FilterMCPRow {
	out := FilterMCPRow{}
	out.ID = model.ID
	out.ScopeType = model.ScopeType
	out.Pattern = model.Pattern
	out.IsRegex = model.IsRegex
	out.Enabled = model.Enabled
	out.SortOrder = model.SortOrder
	return out
}

func (r *FilterMCPRepo) filterMCPModelsToRows(ctx context.Context, models []filterRuleMCPModel) ([]FilterMCPRow, error) {
	result := make([]FilterMCPRow, 0, len(models))
	for _, model := range models {
		row := modelToFilterMCP(model)
		ids, err := r.listBindings(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		row.UpstreamIDs = ids
		result = append(result, row)
	}
	return result, nil
}

func (r *FilterMCPRepo) listBindings(ctx context.Context, ruleID string) ([]string, error) {
	uid, err := parseUUID(ruleID)
	if err != nil {
		return nil, err
	}
	var bindings []filterRuleMCPUpstreamModel
	if err := r.db.WithContext(ctx).Where("rule_id = ?", uid).Order("upstream_id ASC").Find(&bindings).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		ids = append(ids, binding.UpstreamID)
	}
	return ids, nil
}

func (r *FilterMCPRepo) replaceBindings(ctx context.Context, ruleID, scopeType string, upstreamIDs []string) error {
	return replaceFilterMCPBindings(ctx, r.db, ruleID, scopeType, upstreamIDs)
}

func replaceFilterMCPBindings(ctx context.Context, db *gorm.DB, ruleID, scopeType string, upstreamIDs []string) error {
	ruleUID, err := parseUUID(ruleID)
	if err != nil {
		return err
	}
	if err := db.WithContext(ctx).Where("rule_id = ?", ruleUID).Delete(&filterRuleMCPUpstreamModel{}).Error; err != nil {
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
		binding := filterRuleMCPUpstreamModel{RuleID: ruleUID, UpstreamID: upUID}
		err = db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error
		if err != nil {
			return classifyWrite(err, "规则作用范围冲突", "选择的上游 MCP 不存在")
		}
	}
	return nil
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
	db *gorm.DB
}

// NewFilterAPIKeyRepo 构造 API Key 级屏蔽规则仓储。
func NewFilterAPIKeyRepo(db *gorm.DB) *FilterAPIKeyRepo {
	return &FilterAPIKeyRepo{db: db}
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
	err = r.db.WithContext(ctx).Model(&filterRuleAPIKeyModel{}).Create(map[string]any{
		"id":         id,
		"api_key_id": apiKeyID,
		"pattern":    row.Pattern,
		"is_regex":   row.IsRegex,
		"enabled":    row.Enabled,
		"sort_order": row.SortOrder,
	}).Error
	if err != nil {
		return FilterAPIKeyRow{}, classifyWrite(err, "API Key 屏蔽规则冲突", "绑定的 API Key 不存在")
	}
	row.ID = id
	return row, nil
}

// Get 按标识查询单条 API Key 级屏蔽规则；不存在返回 CodeNotFound。
func (r *FilterAPIKeyRepo) Get(ctx context.Context, id string) (FilterAPIKeyRow, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return FilterAPIKeyRow{}, err
	}
	var model filterRuleAPIKeyModel
	if err := r.db.WithContext(ctx).Where("id = ?", uid).First(&model).Error; err != nil {
		return FilterAPIKeyRow{}, notFoundIfNoRows(err, "API Key 屏蔽规则不存在")
	}
	return modelToFilterAPIKey(model), nil
}

// ListByAPIKey 返回某 API Key 的全部屏蔽规则，按 sort_order 升序；无数据返回空切片。
func (r *FilterAPIKeyRepo) ListByAPIKey(ctx context.Context, apiKeyID string) ([]FilterAPIKeyRow, error) {
	uid, err := parseUUID(apiKeyID)
	if err != nil {
		return nil, err
	}
	var models []filterRuleAPIKeyModel
	if err := r.db.WithContext(ctx).Where("api_key_id = ?", uid).Order("sort_order ASC").Find(&models).Error; err != nil {
		return nil, err
	}
	result := make([]FilterAPIKeyRow, 0, len(models))
	for _, model := range models {
		result = append(result, modelToFilterAPIKey(model))
	}
	return result, nil
}

// CountByAPIKey 统计某 API Key 已有的屏蔽规则数量，供应用层做上限校验（Req 13.2/13.3）。
func (r *FilterAPIKeyRepo) CountByAPIKey(ctx context.Context, apiKeyID string) (int, error) {
	uid, err := parseUUID(apiKeyID)
	if err != nil {
		return 0, err
	}
	var n int64
	if err := r.db.WithContext(ctx).Model(&filterRuleAPIKeyModel{}).Where("api_key_id = ?", uid).Count(&n).Error; err != nil {
		return 0, err
	}
	return int(n), nil
}

// Update 更新一条 API Key 级屏蔽规则（不变更绑定的 api_key_id）；不存在返回 CodeNotFound。
func (r *FilterAPIKeyRepo) Update(ctx context.Context, row FilterAPIKeyRow) (FilterAPIKeyRow, error) {
	uid, err := parseUUID(row.ID)
	if err != nil {
		return FilterAPIKeyRow{}, err
	}
	res := r.db.WithContext(ctx).Model(&filterRuleAPIKeyModel{}).Where("id = ?", uid).Updates(map[string]any{
		"pattern":    row.Pattern,
		"is_regex":   row.IsRegex,
		"enabled":    row.Enabled,
		"sort_order": row.SortOrder,
	})
	if res.Error != nil {
		return FilterAPIKeyRow{}, res.Error
	}
	if res.RowsAffected == 0 {
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
	res := r.db.WithContext(ctx).Model(&filterRuleAPIKeyModel{}).Where("id = ?", uid).Update("enabled", enabled)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
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
	res := r.db.WithContext(ctx).Where("id = ?", uid).Delete(&filterRuleAPIKeyModel{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.NewError(domain.CodeNotFound, "API Key 屏蔽规则不存在")
	}
	return nil
}

func modelToFilterAPIKey(model filterRuleAPIKeyModel) FilterAPIKeyRow {
	out := FilterAPIKeyRow{APIKeyID: model.APIKeyID}
	out.ID = model.ID
	out.Pattern = model.Pattern
	out.IsRegex = model.IsRegex
	out.Enabled = model.Enabled
	out.SortOrder = model.SortOrder
	return out
}
