package store

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// APIKey 是 API Key 元数据（api_key 表）的持久层表示。
//
// 鉴权仅使用密钥哈希（KeyHash）等值比对；KeyPlain 为明文密钥，仅用于管理台二次查看/复制
// （自部署场景，Req 12 扩展），不参与鉴权。KeyPrefix 为展示用前缀。
// RateLimit 与 RateWindowS 为可选速率上限配置（Req 21）；QuotaPerDay 与 QuotaPerMonth 为可选周期额度。
// ExpiresAt 为可选有效期（Req 12.6）。
type APIKey struct {
	// ID 为 API Key 唯一标识。
	ID string
	// Name 为名称，长度需在 1 至 100 个字符之间。
	Name string
	// KeyHash 为密钥的哈希字节，用于鉴权时比对。
	KeyHash []byte
	// KeyPlain 为明文密钥，仅供管理台查看/复制（自部署场景），不参与鉴权。
	KeyPlain string
	// KeyPrefix 为展示用前缀（明文的前若干字符）。
	KeyPrefix string
	// Enabled 表示该 Key 是否启用。
	Enabled bool
	// ExpiresAt 为可选有效期；nil 表示永不过期。
	ExpiresAt *time.Time
	// RateLimit 为可选速率上限；nil 表示不限流。
	RateLimit *int
	// RateWindowS 为限流计数窗口秒数；nil 表示未配置。
	RateWindowS *int
	// QuotaPerDay 为每日调用上限；nil 表示不限额。
	QuotaPerDay *int
	// QuotaPerMonth 为每月调用上限；nil 表示不限额。
	QuotaPerMonth *int
	// CreatedAt 为创建时间。
	CreatedAt time.Time
}

// APIKeyRepo 提供 API Key 元数据的类型安全增删查改。
type APIKeyRepo struct {
	db *gorm.DB
}

// NewAPIKeyRepo 构造 API Key 元数据仓储。
func NewAPIKeyRepo(db *gorm.DB) *APIKeyRepo {
	return &APIKeyRepo{db: db}
}

// Create 持久化一条 API Key 元数据并回填生成标识与创建时间（Req 12.1）。
func (r *APIKeyRepo) Create(ctx context.Context, key APIKey) (APIKey, error) {
	id := newUUID()
	err := r.db.WithContext(ctx).Model(&apiKeyModel{}).Create(map[string]any{
		"id":              id,
		"name":            key.Name,
		"key_hash":        key.KeyHash,
		"key_plain":       key.KeyPlain,
		"key_prefix":      key.KeyPrefix,
		"enabled":         key.Enabled,
		"expires_at":      key.ExpiresAt,
		"rate_limit":      key.RateLimit,
		"rate_window_s":   key.RateWindowS,
		"quota_per_day":   key.QuotaPerDay,
		"quota_per_month": key.QuotaPerMonth,
	}).Error
	if err != nil {
		return APIKey{}, classifyWrite(err, "API Key 名称已存在："+key.Name, "API Key 不存在")
	}
	return r.Get(ctx, id)
}

// Get 按标识查询单条 API Key；不存在返回 CodeNotFound（Req 12.7）。
func (r *APIKeyRepo) Get(ctx context.Context, id string) (APIKey, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return APIKey{}, err
	}
	var model apiKeyModel
	if err := r.db.WithContext(ctx).Where("id = ?", uid).First(&model).Error; err != nil {
		return APIKey{}, notFoundIfNoRows(err, "API Key 不存在")
	}
	return modelToAPIKey(model), nil
}

// GetByHash 按密钥哈希查询单条 API Key，供鉴权中间件比对使用；不存在返回 CodeNotFound。
func (r *APIKeyRepo) GetByHash(ctx context.Context, keyHash []byte) (APIKey, error) {
	var model apiKeyModel
	if err := r.db.WithContext(ctx).Where("key_hash = ?", keyHash).Take(&model).Error; err != nil {
		return APIKey{}, notFoundIfNoRows(err, "API Key 不存在")
	}
	return modelToAPIKey(model), nil
}

// List 返回全部 API Key 元数据，按创建时间倒序排列；无数据返回空切片（Req 12.3、12.9）。
//
// 注意：API Key 明文随 key_plain 存储并返回，便于自部署场景下查看与复制；鉴权仍使用 key_hash。
func (r *APIKeyRepo) List(ctx context.Context) ([]APIKey, error) {
	var models []apiKeyModel
	if err := r.db.WithContext(ctx).Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, err
	}
	result := make([]APIKey, 0, len(models))
	for _, model := range models {
		result = append(result, modelToAPIKey(model))
	}
	return result, nil
}

// Update 更新 API Key 的可变元数据（名称、启停、有效期、限流配置）；不存在返回 CodeNotFound。
//
// 密钥哈希与前缀在创建后不可变更，故此处不更新。
func (r *APIKeyRepo) Update(ctx context.Context, key APIKey) (APIKey, error) {
	uid, err := parseUUID(key.ID)
	if err != nil {
		return APIKey{}, err
	}
	res := r.db.WithContext(ctx).Model(&apiKeyModel{}).Where("id = ?", uid).Updates(map[string]any{
		"name":            key.Name,
		"enabled":         key.Enabled,
		"expires_at":      key.ExpiresAt,
		"rate_limit":      key.RateLimit,
		"rate_window_s":   key.RateWindowS,
		"quota_per_day":   key.QuotaPerDay,
		"quota_per_month": key.QuotaPerMonth,
	})
	if res.Error != nil {
		return APIKey{}, classifyWrite(res.Error, "API Key 名称已存在："+key.Name, "API Key 不存在")
	}
	if res.RowsAffected == 0 {
		return APIKey{}, domain.NewError(domain.CodeNotFound, "API Key 不存在")
	}
	return r.Get(ctx, key.ID)
}

// SetEnabled 仅更新 API Key 的启停状态（Req 12.4）；不存在返回 CodeNotFound。
func (r *APIKeyRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	res := r.db.WithContext(ctx).Model(&apiKeyModel{}).Where("id = ?", uid).Update("enabled", enabled)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.NewError(domain.CodeNotFound, "API Key 不存在")
	}
	return nil
}

// Delete 删除一条 API Key；其从属屏蔽规则与 ACL 通过外键 ON DELETE CASCADE 级联清理（Req 12.2）。
//   - 标识不存在返回 CodeNotFound（Req 12.7）。
func (r *APIKeyRepo) Delete(ctx context.Context, id string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	res := r.db.WithContext(ctx).Where("id = ?", uid).Delete(&apiKeyModel{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.NewError(domain.CodeNotFound, "API Key 不存在")
	}
	return nil
}

func modelToAPIKey(model apiKeyModel) APIKey {
	return APIKey{
		ID:            model.ID,
		Name:          model.Name,
		KeyHash:       model.KeyHash,
		KeyPlain:      model.KeyPlain,
		KeyPrefix:     model.KeyPrefix,
		Enabled:       model.Enabled,
		ExpiresAt:     model.ExpiresAt,
		RateLimit:     model.RateLimit,
		RateWindowS:   model.RateWindowS,
		QuotaPerDay:   model.QuotaPerDay,
		QuotaPerMonth: model.QuotaPerMonth,
		CreatedAt:     model.CreatedAt,
	}
}
