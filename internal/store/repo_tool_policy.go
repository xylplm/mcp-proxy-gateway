package store

import (
	"context"
	"encoding/json"
	"strings"

	"gorm.io/gorm"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// ToolPolicyRepo 提供工具策略规则的类型安全增删查改。
type ToolPolicyRepo struct {
	db *gorm.DB
}

// NewToolPolicyRepo 构造工具策略规则仓储。
func NewToolPolicyRepo(db *gorm.DB) *ToolPolicyRepo {
	return &ToolPolicyRepo{db: db}
}

func (r *ToolPolicyRepo) Create(ctx context.Context, rule domain.ToolPolicyRule) (domain.ToolPolicyRule, error) {
	id := newUUID()
	model, err := toolPolicyToModel(rule)
	if err != nil {
		return domain.ToolPolicyRule{}, err
	}
	model.ID = id
	if err := r.db.WithContext(ctx).Model(&toolPolicyRuleModel{}).Create(map[string]any{
		"id":                model.ID,
		"pattern":           model.Pattern,
		"is_regex":          model.IsRegex,
		"enabled":           model.Enabled,
		"sort_order":        model.SortOrder,
		"routing_strategy":  model.RoutingStrategy,
		"cache_enabled":     model.CacheEnabled,
		"cache_ttl_seconds": model.CacheTTLSeconds,
		"risk_tags":         model.RiskTags,
		"ignored_risk_tags": model.IgnoredRiskTags,
	}).Error; err != nil {
		return domain.ToolPolicyRule{}, classifyWrite(err, "工具策略规则冲突", "工具策略规则创建失败")
	}
	return modelToToolPolicy(model)
}

func (r *ToolPolicyRepo) Get(ctx context.Context, id string) (domain.ToolPolicyRule, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return domain.ToolPolicyRule{}, err
	}
	var model toolPolicyRuleModel
	if err := r.db.WithContext(ctx).Where("id = ?", uid).First(&model).Error; err != nil {
		return domain.ToolPolicyRule{}, notFoundIfNoRows(err, "工具策略规则不存在")
	}
	return modelToToolPolicy(model)
}

func (r *ToolPolicyRepo) List(ctx context.Context) ([]domain.ToolPolicyRule, error) {
	var models []toolPolicyRuleModel
	if err := r.db.WithContext(ctx).Order("sort_order ASC").Order("created_at ASC").Find(&models).Error; err != nil {
		return nil, err
	}
	out := make([]domain.ToolPolicyRule, 0, len(models))
	for _, model := range models {
		rule, err := modelToToolPolicy(model)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}

func (r *ToolPolicyRepo) Count(ctx context.Context) (int, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&toolPolicyRuleModel{}).Count(&n).Error; err != nil {
		return 0, err
	}
	return int(n), nil
}

func (r *ToolPolicyRepo) Update(ctx context.Context, rule domain.ToolPolicyRule) (domain.ToolPolicyRule, error) {
	uid, err := parseUUID(rule.ID)
	if err != nil {
		return domain.ToolPolicyRule{}, err
	}
	model, err := toolPolicyToModel(rule)
	if err != nil {
		return domain.ToolPolicyRule{}, err
	}
	res := r.db.WithContext(ctx).Model(&toolPolicyRuleModel{}).Where("id = ?", uid).Updates(map[string]any{
		"pattern":           model.Pattern,
		"is_regex":          model.IsRegex,
		"enabled":           model.Enabled,
		"sort_order":        model.SortOrder,
		"routing_strategy":  model.RoutingStrategy,
		"cache_enabled":     model.CacheEnabled,
		"cache_ttl_seconds": model.CacheTTLSeconds,
		"risk_tags":         model.RiskTags,
		"ignored_risk_tags": model.IgnoredRiskTags,
	})
	if res.Error != nil {
		return domain.ToolPolicyRule{}, res.Error
	}
	if res.RowsAffected == 0 {
		return domain.ToolPolicyRule{}, domain.NewError(domain.CodeNotFound, "工具策略规则不存在")
	}
	model.ID = uid
	return modelToToolPolicy(model)
}

func (r *ToolPolicyRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	res := r.db.WithContext(ctx).Model(&toolPolicyRuleModel{}).Where("id = ?", uid).Update("enabled", enabled)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.NewError(domain.CodeNotFound, "工具策略规则不存在")
	}
	return nil
}

func (r *ToolPolicyRepo) Delete(ctx context.Context, id string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	res := r.db.WithContext(ctx).Where("id = ?", uid).Delete(&toolPolicyRuleModel{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.NewError(domain.CodeNotFound, "工具策略规则不存在")
	}
	return nil
}

func toolPolicyToModel(rule domain.ToolPolicyRule) (toolPolicyRuleModel, error) {
	rawTags, err := json.Marshal(normalizeRiskTags(rule.RiskTags))
	if err != nil {
		return toolPolicyRuleModel{}, err
	}
	rawIgnoredTags, err := json.Marshal(normalizeIgnoredRiskTags(rule.IgnoredRiskTags))
	if err != nil {
		return toolPolicyRuleModel{}, err
	}
	return toolPolicyRuleModel{
		ID:              rule.ID,
		Pattern:         rule.Pattern,
		IsRegex:         rule.IsRegex,
		Enabled:         rule.Enabled,
		SortOrder:       rule.SortOrder,
		RoutingStrategy: string(rule.RoutingStrategy),
		CacheEnabled:    rule.CacheEnabled,
		CacheTTLSeconds: rule.CacheTTLSeconds,
		RiskTags:        JSONB(rawTags),
		IgnoredRiskTags: JSONB(rawIgnoredTags),
	}, nil
}

func modelToToolPolicy(model toolPolicyRuleModel) (domain.ToolPolicyRule, error) {
	var tags []string
	if len(model.RiskTags) > 0 {
		if err := json.Unmarshal(model.RiskTags, &tags); err != nil {
			return domain.ToolPolicyRule{}, err
		}
	}
	var ignoredTags []string
	if len(model.IgnoredRiskTags) > 0 {
		if err := json.Unmarshal(model.IgnoredRiskTags, &ignoredTags); err != nil {
			return domain.ToolPolicyRule{}, err
		}
	}
	return domain.ToolPolicyRule{
		ID:              model.ID,
		Pattern:         model.Pattern,
		IsRegex:         model.IsRegex,
		Enabled:         model.Enabled,
		SortOrder:       model.SortOrder,
		RoutingStrategy: domain.ToolRoutingStrategy(model.RoutingStrategy),
		CacheEnabled:    model.CacheEnabled,
		CacheTTLSeconds: model.CacheTTLSeconds,
		RiskTags:        normalizeRiskTags(tags),
		IgnoredRiskTags: normalizeIgnoredRiskTags(ignoredTags),
	}, nil
}

func normalizeRiskTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
		if len(out) >= domain.MaxToolPolicyRiskTags {
			break
		}
	}
	return out
}

func normalizeIgnoredRiskTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	allowed := map[string]struct{}{
		"payment": {},
		"delete":  {},
		"write":   {},
		"send":    {},
	}
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		if _, ok := allowed[tag]; !ok {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
		if len(out) >= domain.MaxToolPolicyIgnoredRiskTags {
			break
		}
	}
	return out
}
