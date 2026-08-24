package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/risk"
)

type ToolRiskRepo struct{ db *gorm.DB }

func NewToolRiskRepo(db *gorm.DB) *ToolRiskRepo { return &ToolRiskRepo{db: db} }

type RiskListQuery struct {
	UpstreamID    string
	Status        risk.Status
	Keyword       string
	Level         risk.Level
	ManualOnly    bool
	MinConfidence *float64
	Page          int
	PageSize      int
}

type RiskListResult struct {
	Items   []risk.Assessment `json:"items"`
	Total   int64             `json:"total"`
	Page    int               `json:"page"`
	Size    int               `json:"pageSize"`
	Summary RiskSummary       `json:"summary"`
}

type RiskSummary struct {
	Total       int `json:"total"`
	Low         int `json:"low"`
	Medium      int `json:"medium"`
	High        int `json:"high"`
	Blocked     int `json:"blocked"`
	NeedsReview int `json:"needsReview"`
}

type ReconcileResult struct {
	Added   int `json:"added"`
	Changed int `json:"changed"`
	Removed int `json:"removed"`
	Current int `json:"current"`
}

type RiskOverrideTarget struct {
	UpstreamID   string
	OriginalName string
}

// computeEffectiveLevel 从已有字段计算有效风险等级，与 risk.EffectiveLevel 语义一致，
// 但直接操作 model 字段，供写路径计算后持久化到 effective_level 列。
func computeEffectiveLevel(status string, aiLevel, manualLevel *string, floor string) string {
	if status != string(risk.StatusRated) && status != string(risk.StatusNeedsReview) {
		return string(risk.LevelHigh)
	}
	if manualLevel != nil && risk.ValidLevel(risk.Level(*manualLevel)) {
		return *manualLevel
	}
	if aiLevel == nil || !risk.ValidLevel(risk.Level(*aiLevel)) {
		return string(risk.LevelHigh)
	}
	f := risk.Level(floor)
	if !risk.ValidLevel(f) {
		f = risk.LevelLow
	}
	return string(risk.MaxLevel(risk.Level(*aiLevel), f))
}

func (r *ToolRiskRepo) Get(ctx context.Context, upstreamID, originalName string) (risk.Assessment, error) {
	var model toolRiskAssessmentModel
	err := r.db.WithContext(ctx).Where("upstream_id = ? AND original_name = ?", upstreamID, originalName).First(&model).Error
	if err != nil {
		return risk.Assessment{}, notFoundIfNoRows(err, "工具风险记录不存在")
	}
	return modelToAssessment(model), nil
}

func (r *ToolRiskRepo) ListByUpstream(ctx context.Context, upstreamID string) ([]risk.Assessment, error) {
	var models []toolRiskAssessmentModel
	if err := r.db.WithContext(ctx).Where("upstream_id = ? AND status <> ?", upstreamID, risk.StatusRemoved).Find(&models).Error; err != nil {
		return nil, err
	}
	return modelsToAssessments(models), nil
}

func (r *ToolRiskRepo) ListAssessable(ctx context.Context, limit int) ([]risk.Assessment, error) {
	if limit < 1 || limit > 1000 {
		limit = 500
	}
	var models []toolRiskAssessmentModel
	err := r.db.WithContext(ctx).Where("status IN ?", []risk.Status{risk.StatusPending, risk.StatusStale, risk.StatusError}).
		Order("updated_at ASC").Limit(limit).Find(&models).Error
	if err != nil {
		return nil, err
	}
	return modelsToAssessments(models), nil
}

func (r *ToolRiskRepo) ListNeedsReview(ctx context.Context, limit int) ([]risk.Assessment, error) {
	if limit < 1 || limit > 1000 {
		limit = 500
	}
	var models []toolRiskAssessmentModel
	err := r.db.WithContext(ctx).
		Where("status = ? AND manual_level IS NULL", risk.StatusNeedsReview).
		Order("updated_at ASC").Limit(limit).Find(&models).Error
	if err != nil {
		return nil, err
	}
	return modelsToAssessments(models), nil
}

func (r *ToolRiskRepo) ListAll(ctx context.Context) ([]risk.Assessment, error) {
	var models []toolRiskAssessmentModel
	if err := r.db.WithContext(ctx).Order("created_at ASC").Find(&models).Error; err != nil {
		return nil, err
	}
	return modelsToAssessments(models), nil
}

func (r *ToolRiskRepo) Restore(ctx context.Context, item risk.Assessment) (risk.Assessment, error) {
	aiTags, _ := json.Marshal(item.AITags)
	reviewReasons, _ := json.Marshal(item.ReviewReasons)
	manualTags, _ := json.Marshal(item.ManualTags)
	var aiLevel, manualLevel *string
	if risk.ValidLevel(item.AILevel) {
		value := string(item.AILevel)
		aiLevel = &value
	}
	if item.ManualConfirmed && risk.ValidLevel(item.ManualLevel) {
		value := string(item.ManualLevel)
		manualLevel = &value
	}
	var providerID *string
	if item.ProviderID != "" {
		providerID = &item.ProviderID
	}
	now := time.Now().UTC()
	created := item.CreatedAt
	if created.IsZero() {
		created = now
	}
	model := toolRiskAssessmentModel{ID: newUUID(), UpstreamID: item.UpstreamID, OriginalName: item.OriginalName,
		ExposedNameSnapshot: item.ExposedName, DescriptionSnapshot: item.Description, DescriptionZhSnapshot: item.DescriptionZh, InputSchemaSnapshot: schemaSnapshot(item.InputSchema), SchemaFingerprint: item.Fingerprint,
		DeterministicFloor: string(item.Floor), RuleVersion: item.RuleVersion, AILevel: aiLevel, AITags: JSONB(aiTags),
		AIConfidence: item.AIConfidence, AIReason: item.AIReason, ReviewReasons: JSONB(reviewReasons), ProviderID: providerID, ProviderNameSnapshot: item.ProviderName,
		ModelSnapshot: item.Model, PromptVersion: item.PromptVersion, Status: string(item.Status), LastError: item.LastError,
		ManualLevel: manualLevel, ManualTags: JSONB(manualTags), ManualReason: item.ManualReason,
		ManualForceDowngrade: item.ManualForce, ReviewedAt: item.ReviewedAt, AssessedAt: item.AssessedAt,
		EffectiveLevel: string(risk.EffectiveLevel(item)),
		CreatedAt:      created, UpdatedAt: now}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return risk.Assessment{}, err
	}
	return modelToAssessment(model), nil
}

func (r *ToolRiskRepo) ApplyAIResult(ctx context.Context, upstreamID, originalName string, result risk.AIResult, provider risk.Provider) (risk.Assessment, error) {
	current, err := r.Get(ctx, upstreamID, originalName)
	if err != nil {
		return risk.Assessment{}, err
	}
	status := risk.StatusRated
	reviewReasons := risk.ReviewReasonsFor(result, current.Floor)
	if len(reviewReasons) > 0 {
		status = risk.StatusNeedsReview
	}
	tags, _ := json.Marshal(result.RiskTags)
	encodedReviewReasons, _ := json.Marshal(reviewReasons)
	aiLevelStr := string(result.RiskLevel)
	// 若该工具已有人工覆盖，effective_level 应继续以 manual_level 为准，
	// AI 评级结果不得覆盖已确认的人工结论。
	var manualLevelPtr *string
	if current.ManualConfirmed && risk.ValidLevel(current.ManualLevel) {
		s := string(current.ManualLevel)
		manualLevelPtr = &s
	}
	effectiveLevel := computeEffectiveLevel(string(status), &aiLevelStr, manualLevelPtr, string(current.Floor))
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).Model(&toolRiskAssessmentModel{}).
		Where("upstream_id = ? AND original_name = ?", upstreamID, originalName).
		Updates(map[string]any{"ai_level": result.RiskLevel, "ai_tags": JSONB(tags), "ai_confidence": result.Confidence,
			"ai_reason": result.Reason, "review_reasons": JSONB(encodedReviewReasons), "description_zh_snapshot": result.FunctionSummaryZh, "provider_id": provider.ID, "provider_name_snapshot": provider.Name,
			"model_snapshot": provider.Model, "prompt_version": risk.PromptVersion, "status": status,
			"effective_level": effectiveLevel, "last_error": "", "assessed_at": now, "updated_at": now})
	if res.Error != nil {
		return risk.Assessment{}, res.Error
	}
	return r.Get(ctx, upstreamID, originalName)
}

func (r *ToolRiskRepo) MarkAIError(ctx context.Context, upstreamID, originalName string, message string) error {
	// 按 rune 截断，避免按字节截断切断多字节 UTF-8 字符导致写入非法编码。
	if len(message) > 500 {
		runes := []rune(message)
		if len(runes) > 200 {
			runes = runes[:200]
		}
		message = string(runes)
		if len(message) > 500 {
			// 极端情况下 200 个宽字符仍超过 500 字节，再按字节二次截断到安全边界
			// 并补全至最近有效 UTF-8 字符边界。
			message = message[:500]
			for len(message) > 0 && message[len(message)-1]>>6 == 0b10 {
				message = message[:len(message)-1]
			}
		}
	}
	return r.db.WithContext(ctx).Model(&toolRiskAssessmentModel{}).
		Where("upstream_id = ? AND original_name = ?", upstreamID, originalName).
		Updates(map[string]any{
			"status":          risk.StatusError,
			"effective_level": string(risk.LevelHigh),
			"last_error":      message,
			"updated_at":      time.Now().UTC(),
		}).Error
}

func (r *ToolRiskRepo) List(ctx context.Context, q RiskListQuery) (RiskListResult, error) {
	summary, err := r.summary(ctx)
	if err != nil {
		return RiskListResult{}, err
	}
	db := r.db.WithContext(ctx).Model(&toolRiskAssessmentModel{})
	if q.UpstreamID != "" {
		db = db.Where("upstream_id = ?", q.UpstreamID)
	}
	if q.Status != "" {
		db = db.Where("status = ?", q.Status)
	}
	if q.Keyword != "" {
		term := "%" + strings.ToLower(q.Keyword) + "%"
		db = db.Where("LOWER(original_name) LIKE ? OR LOWER(exposed_name_snapshot) LIKE ? OR LOWER(description_snapshot) LIKE ?", term, term, term)
	}
	if q.ManualOnly {
		db = db.Where("manual_level IS NOT NULL")
	}
	if q.MinConfidence != nil {
		db = db.Where("ai_confidence >= ?", *q.MinConfidence)
	}
	// effective_level 已持久化，直接走索引过滤，不再全表拉到应用层计算。
	if q.Level != "" {
		db = db.Where("effective_level = ?", string(q.Level))
	}
	page, size := q.Page, q.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return RiskListResult{}, err
	}
	var models []toolRiskAssessmentModel
	if err := db.Order("updated_at DESC").Offset((page - 1) * size).Limit(size).Find(&models).Error; err != nil {
		return RiskListResult{}, err
	}
	return RiskListResult{Items: modelsToAssessments(models), Total: total, Page: page, Size: size, Summary: summary}, nil
}

func (r *ToolRiskRepo) summary(ctx context.Context) (RiskSummary, error) {
	// 单次查询同时聚合等级分布和待复核数，避免两次独立查询之间的轻微不一致。
	type levelAgg struct {
		EffectiveLevel string `gorm:"column:effective_level"`
		Count          int    `gorm:"column:cnt"`
		NeedsReview    int    `gorm:"column:needs_review_cnt"`
	}
	var rows []levelAgg
	err := r.db.WithContext(ctx).
		Model(&toolRiskAssessmentModel{}).
		Select("effective_level, COUNT(*) AS cnt, COUNT(*) FILTER (WHERE status = ?) AS needs_review_cnt", string(risk.StatusNeedsReview)).
		Where("status <> ?", string(risk.StatusRemoved)).
		Group("effective_level").
		Scan(&rows).Error
	if err != nil {
		return RiskSummary{}, err
	}
	var s RiskSummary
	for _, row := range rows {
		s.Total += row.Count
		s.NeedsReview += row.NeedsReview
		switch risk.Level(row.EffectiveLevel) {
		case risk.LevelLow:
			s.Low = row.Count
		case risk.LevelMedium:
			s.Medium = row.Count
		case risk.LevelHigh:
			s.High = row.Count
		case risk.LevelBlocked:
			s.Blocked = row.Count
		}
	}
	return s, nil
}

func (r *ToolRiskRepo) Reconcile(ctx context.Context, upstreamID string, tools []domain.ToolDef) (ReconcileResult, error) {
	result := ReconcileResult{Current: len(tools)}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing []toolRiskAssessmentModel
		if err := tx.Where("upstream_id = ?", upstreamID).Find(&existing).Error; err != nil {
			return err
		}
		byName := make(map[string]toolRiskAssessmentModel, len(existing))
		for _, model := range existing {
			byName[model.OriginalName] = model
		}
		seen := make(map[string]struct{}, len(tools))
		now := time.Now().UTC()
		for _, tool := range tools {
			tool.UpstreamID = upstreamID
			fingerprint, err := risk.ToolFingerprint(tool)
			if err != nil {
				return err
			}
			deterministic := risk.DeterministicAssessment(tool.OriginalName, tool.Description)
			seen[tool.OriginalName] = struct{}{}
			old, ok := byName[tool.OriginalName]
			if !ok {
				pendingStatus := string(risk.StatusPending)
				model := toolRiskAssessmentModel{ID: newUUID(), UpstreamID: upstreamID, OriginalName: tool.OriginalName,
					ExposedNameSnapshot: tool.Name, DescriptionSnapshot: tool.Description, InputSchemaSnapshot: schemaSnapshot(tool.InputSchema), SchemaFingerprint: fingerprint,
					DeterministicFloor: string(deterministic.Floor), RuleVersion: risk.RuleVersion,
					AITags: JSONB(`[]`), ReviewReasons: JSONB(`[]`), ManualTags: JSONB(`[]`),
					Status:         pendingStatus,
					EffectiveLevel: computeEffectiveLevel(pendingStatus, nil, nil, string(deterministic.Floor)),
					CreatedAt:      now, UpdatedAt: now}
				if err := tx.Create(&model).Error; err != nil {
					return err
				}
				result.Added++
				continue
			}
			status := old.Status
			if old.SchemaFingerprint != fingerprint || old.RuleVersion != risk.RuleVersion {
				status = string(risk.StatusStale)
				result.Changed++
			} else if old.Status == string(risk.StatusRemoved) {
				status = string(risk.StatusPending)
				result.Changed++
			}
			if err := tx.Model(&toolRiskAssessmentModel{}).Where("id = ?", old.ID).Updates(map[string]any{
				"exposed_name_snapshot": tool.Name, "description_snapshot": tool.Description,
				"input_schema_snapshot": schemaSnapshot(tool.InputSchema),
				"schema_fingerprint":    fingerprint, "deterministic_floor": deterministic.Floor,
				"rule_version":    risk.RuleVersion,
				"status":          status,
				"effective_level": computeEffectiveLevel(status, old.AILevel, old.ManualLevel, string(deterministic.Floor)),
				"updated_at":      now,
			}).Error; err != nil {
				return err
			}
		}
		for _, old := range existing {
			if _, ok := seen[old.OriginalName]; ok || old.Status == string(risk.StatusRemoved) {
				continue
			}
			if err := tx.Model(&toolRiskAssessmentModel{}).Where("id = ?", old.ID).Updates(map[string]any{
				"status":          risk.StatusRemoved,
				"effective_level": string(risk.LevelHigh),
				"updated_at":      now,
			}).Error; err != nil {
				return err
			}
			result.Removed++
		}
		return nil
	})
	return result, err
}

func (r *ToolRiskRepo) SetManualOverride(ctx context.Context, upstreamID, originalName string, level risk.Level, tags []string, reason string, force bool) (risk.Assessment, error) {
	items, err := r.BulkSetManualOverride(ctx, []RiskOverrideTarget{{UpstreamID: upstreamID, OriginalName: originalName}}, level, tags, reason, force)
	if err != nil {
		return risk.Assessment{}, err
	}
	return items[0], nil
}

func (r *ToolRiskRepo) BulkSetManualOverride(ctx context.Context, targets []RiskOverrideTarget, level risk.Level, tags []string, reason string, force bool) ([]risk.Assessment, error) {
	if len(targets) == 0 {
		return nil, domain.NewError(domain.CodeValidation, "批量覆写目标不能为空")
	}
	encoded, _ := json.Marshal(tags)
	now := time.Now().UTC()
	updated := make([]risk.Assessment, 0, len(targets))
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		models := make([]toolRiskAssessmentModel, 0, len(targets))
		seen := make(map[string]struct{}, len(targets))
		for _, target := range targets {
			key := target.UpstreamID + "\x00" + target.OriginalName
			if _, exists := seen[key]; exists {
				return domain.NewError(domain.CodeValidation, "批量覆写包含重复工具")
			}
			seen[key] = struct{}{}
			var model toolRiskAssessmentModel
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("upstream_id = ? AND original_name = ?", target.UpstreamID, target.OriginalName).First(&model).Error; err != nil {
				return notFoundIfNoRows(err, "工具风险记录不存在")
			}
			if err := risk.ValidateManualOverride(level, risk.Level(model.DeterministicFloor), force, reason); err != nil {
				return domain.NewError(domain.CodeValidation, model.OriginalName+"："+err.Error())
			}
			models = append(models, model)
		}
		for _, model := range models {
			newStatus := string(risk.StatusRated)
			levelStr := string(level)
			effectiveLevel := computeEffectiveLevel(newStatus, model.AILevel, &levelStr, model.DeterministicFloor)
			res := tx.Model(&toolRiskAssessmentModel{}).Where("id = ?", model.ID).
				Updates(map[string]any{
					"manual_level":           level,
					"manual_tags":            JSONB(encoded),
					"manual_reason":          strings.TrimSpace(reason),
					"status":                 newStatus,
					"effective_level":        effectiveLevel,
					"manual_force_downgrade": force,
					"reviewed_at":            now,
					"updated_at":             now,
				})
			if res.Error != nil {
				return res.Error
			}
			var saved toolRiskAssessmentModel
			if err := tx.Where("id = ?", model.ID).First(&saved).Error; err != nil {
				return err
			}
			updated = append(updated, modelToAssessment(saved))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (r *ToolRiskRepo) ClearManualOverride(ctx context.Context, upstreamID, originalName string) (risk.Assessment, error) {
	var updated risk.Assessment
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model toolRiskAssessmentModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("upstream_id = ? AND original_name = ?", upstreamID, originalName).
			First(&model).Error; err != nil {
			return notFoundIfNoRows(err, "工具风险记录不存在")
		}
		status := risk.StatusAfterClearingManualOverride(modelToAssessment(model))
		effectiveLevel := computeEffectiveLevel(string(status), model.AILevel, nil, model.DeterministicFloor)
		if err := tx.Model(&toolRiskAssessmentModel{}).Where("id = ?", model.ID).
			Updates(map[string]any{
				"manual_level":           nil,
				"manual_tags":            JSONB(`[]`),
				"manual_reason":          "",
				"manual_force_downgrade": false,
				"reviewed_at":            nil,
				"status":                 status,
				"effective_level":        effectiveLevel,
				"updated_at":             time.Now().UTC(),
			}).Error; err != nil {
			return err
		}
		var saved toolRiskAssessmentModel
		if err := tx.Where("id = ?", model.ID).First(&saved).Error; err != nil {
			return err
		}
		updated = modelToAssessment(saved)
		return nil
	})
	if err != nil {
		return risk.Assessment{}, err
	}
	return updated, nil
}

func modelsToAssessments(models []toolRiskAssessmentModel) []risk.Assessment {
	out := make([]risk.Assessment, 0, len(models))
	for _, model := range models {
		out = append(out, modelToAssessment(model))
	}
	return out
}

func modelToAssessment(m toolRiskAssessmentModel) risk.Assessment {
	var aiTags, manualTags []string
	var reviewReasons []risk.ReviewReason
	_ = json.Unmarshal(m.AITags, &aiTags)
	_ = json.Unmarshal(m.ManualTags, &manualTags)
	_ = json.Unmarshal(m.ReviewReasons, &reviewReasons)
	if len(reviewReasons) == 0 && m.Status == string(risk.StatusNeedsReview) {
		if m.AIConfidence != nil && *m.AIConfidence < 0.80 {
			reviewReasons = append(reviewReasons, risk.ReviewReasonLowConfidence)
		}
		if m.AILevel != nil && risk.MaxLevel(risk.Level(*m.AILevel), risk.Level(m.DeterministicFloor)) != risk.Level(*m.AILevel) {
			reviewReasons = append(reviewReasons, risk.ReviewReasonBelowRuleFloor)
		}
		if len(reviewReasons) == 0 {
			reviewReasons = append(reviewReasons, risk.ReviewReasonLegacyAIRequest)
		}
	}
	a := risk.Assessment{ID: m.ID, UpstreamID: m.UpstreamID, OriginalName: m.OriginalName,
		ExposedName: m.ExposedNameSnapshot, Description: m.DescriptionSnapshot, DescriptionZh: m.DescriptionZhSnapshot, InputSchema: append([]byte(nil), m.InputSchemaSnapshot...), Fingerprint: m.SchemaFingerprint,
		Floor: risk.Level(m.DeterministicFloor), RuleVersion: m.RuleVersion, AITags: aiTags,
		AIConfidence: m.AIConfidence, AIReason: m.AIReason, ReviewReasons: reviewReasons, ProviderName: m.ProviderNameSnapshot,
		Model: m.ModelSnapshot, PromptVersion: m.PromptVersion, Status: risk.Status(m.Status), LastError: m.LastError,
		ManualTags: manualTags, ManualReason: m.ManualReason, ManualForce: m.ManualForceDowngrade,
		ReviewedAt: m.ReviewedAt, AssessedAt: m.AssessedAt, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
	if m.AILevel != nil {
		a.AILevel = risk.Level(*m.AILevel)
	}
	if m.ManualLevel != nil {
		a.ManualLevel, a.ManualConfirmed = risk.Level(*m.ManualLevel), true
	}
	if m.ProviderID != nil {
		a.ProviderID = *m.ProviderID
	}
	a.Effective = risk.EffectiveLevel(a)
	return a
}

func schemaSnapshot(raw json.RawMessage) JSONB {
	if len(raw) == 0 || !json.Valid(raw) {
		return JSONB(`{}`)
	}
	return JSONB(append([]byte(nil), raw...))
}
