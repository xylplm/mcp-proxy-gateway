package store

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/risk"
)

type AIProviderRepo struct{ db *gorm.DB }

func NewAIProviderRepo(db *gorm.DB) *AIProviderRepo { return &AIProviderRepo{db: db} }

func (r *AIProviderRepo) Create(ctx context.Context, p risk.Provider) (risk.Provider, error) {
	now := time.Now().UTC()
	model := providerToModel(p)
	model.ID = newUUID()
	model.CreatedAt, model.UpdatedAt = now, now
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return risk.Provider{}, classifyWrite(err, "AI Provider 名称已存在："+p.Name, "AI Provider 不存在")
	}
	return modelToProvider(model), nil
}

func (r *AIProviderRepo) Update(ctx context.Context, p risk.Provider) (risk.Provider, error) {
	uid, err := parseUUID(p.ID)
	if err != nil {
		return risk.Provider{}, err
	}
	updates := map[string]any{
		"name": p.Name, "base_url": p.BaseURL, "api_style": p.APIStyle, "model": p.Model,
		"enabled":   p.Enabled,
		"timeout_s": p.TimeoutS, "batch_size": p.BatchSize, "max_concurrency": p.MaxConcurrency,
		"auto_assess": p.AutoAssess, "updated_at": time.Now().UTC(),
	}
	if !p.Enabled {
		updates["active"] = false
	}
	if p.APIKeyCiphertext != nil {
		updates["api_key_ciphertext"] = p.APIKeyCiphertext
		updates["api_key_nonce"] = p.APIKeyNonce
	}
	res := r.db.WithContext(ctx).Model(&aiProviderModel{}).Where("id = ?", uid).Updates(updates)
	if res.Error != nil {
		return risk.Provider{}, classifyWrite(res.Error, "AI Provider 名称已存在："+p.Name, "AI Provider 不存在")
	}
	if res.RowsAffected == 0 {
		return risk.Provider{}, domain.NewError(domain.CodeNotFound, "AI Provider 不存在")
	}
	return r.Get(ctx, p.ID)
}

func (r *AIProviderRepo) Get(ctx context.Context, id string) (risk.Provider, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return risk.Provider{}, err
	}
	var model aiProviderModel
	if err := r.db.WithContext(ctx).Where("id = ?", uid).First(&model).Error; err != nil {
		return risk.Provider{}, notFoundIfNoRows(err, "AI Provider 不存在")
	}
	return modelToProvider(model), nil
}

func (r *AIProviderRepo) Active(ctx context.Context) (risk.Provider, error) {
	var model aiProviderModel
	if err := r.db.WithContext(ctx).Where("active = ? AND enabled = ?", true, true).First(&model).Error; err != nil {
		return risk.Provider{}, notFoundIfNoRows(err, "没有启用的 AI Provider")
	}
	return modelToProvider(model), nil
}

func (r *AIProviderRepo) List(ctx context.Context) ([]risk.Provider, error) {
	var models []aiProviderModel
	if err := r.db.WithContext(ctx).Order("active DESC, created_at DESC").Find(&models).Error; err != nil {
		return nil, err
	}
	out := make([]risk.Provider, 0, len(models))
	for _, model := range models {
		out = append(out, modelToProvider(model))
	}
	return out, nil
}

func (r *AIProviderRepo) Activate(ctx context.Context, id string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&aiProviderModel{}).Where("active = ?", true).Update("active", false).Error; err != nil {
			return err
		}
		res := tx.Model(&aiProviderModel{}).Where("id = ? AND enabled = ?", uid, true).Update("active", true)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return domain.NewError(domain.CodeNotFound, "AI Provider 不存在或未启用")
		}
		return nil
	})
}

func (r *AIProviderRepo) Delete(ctx context.Context, id string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	res := r.db.WithContext(ctx).Where("id = ?", uid).Delete(&aiProviderModel{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.NewError(domain.CodeNotFound, "AI Provider 不存在")
	}
	return nil
}

func providerToModel(p risk.Provider) aiProviderModel {
	return aiProviderModel{ID: p.ID, Name: p.Name, BaseURL: p.BaseURL, APIStyle: string(p.APIStyle), Model: p.Model,
		APIKeyCiphertext: p.APIKeyCiphertext, APIKeyNonce: p.APIKeyNonce, Enabled: p.Enabled, Active: p.Active,
		TimeoutS: p.TimeoutS, BatchSize: p.BatchSize,
		MaxConcurrency: p.MaxConcurrency, AutoAssess: p.AutoAssess, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

func modelToProvider(m aiProviderModel) risk.Provider {
	hasKey := len(m.APIKeyCiphertext) > 0 && len(m.APIKeyNonce) > 0
	masked := ""
	if hasKey {
		masked = "********"
	}
	return risk.Provider{ID: m.ID, Name: m.Name, BaseURL: m.BaseURL, APIStyle: risk.APIStyle(m.APIStyle), Model: m.Model,
		APIKeyCiphertext: append([]byte(nil), m.APIKeyCiphertext...), APIKeyNonce: append([]byte(nil), m.APIKeyNonce...),
		HasAPIKey: hasKey, APIKeyMasked: masked, Enabled: m.Enabled, Active: m.Active,
		TimeoutS: m.TimeoutS, BatchSize: m.BatchSize,
		MaxConcurrency: m.MaxConcurrency, AutoAssess: m.AutoAssess, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
}
