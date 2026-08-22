package store

import (
	"context"
	"sort"

	"gorm.io/gorm"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// APIKeyUpstreamAccess 是单个 API Key 的上游访问范围快照。
type APIKeyUpstreamAccess struct {
	Mode        string   `json:"mode"`
	UpstreamIDs []string `json:"upstreamIds"`
}

// APIKeyUpstreamAccessRepo 原子维护 API Key 的访问模式与已选上游。
type APIKeyUpstreamAccessRepo struct {
	db *gorm.DB
}

func NewAPIKeyUpstreamAccessRepo(db *gorm.DB) *APIKeyUpstreamAccessRepo {
	return &APIKeyUpstreamAccessRepo{db: db}
}

func (r *APIKeyUpstreamAccessRepo) Get(ctx context.Context, apiKeyID string) (APIKeyUpstreamAccess, error) {
	uid, err := parseUUID(apiKeyID)
	if err != nil {
		return APIKeyUpstreamAccess{}, err
	}
	var key apiKeyModel
	if err := r.db.WithContext(ctx).Select("id", "upstream_access_mode").Where("id = ?", uid).First(&key).Error; err != nil {
		return APIKeyUpstreamAccess{}, notFoundIfNoRows(err, "API Key 不存在")
	}
	var rows []apiKeyUpstreamAccessModel
	if err := r.db.WithContext(ctx).Where("api_key_id = ?", uid).Find(&rows).Error; err != nil {
		return APIKeyUpstreamAccess{}, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.UpstreamID)
	}
	sort.Strings(ids)
	return APIKeyUpstreamAccess{Mode: normalizeUpstreamAccessMode(key.UpstreamAccessMode), UpstreamIDs: ids}, nil
}

func (r *APIKeyUpstreamAccessRepo) Replace(ctx context.Context, apiKeyID, mode string, upstreamIDs []string) error {
	uid, err := parseUUID(apiKeyID)
	if err != nil {
		return err
	}
	parsed := make([]string, 0, len(upstreamIDs))
	seen := make(map[string]struct{}, len(upstreamIDs))
	for _, id := range upstreamIDs {
		id, err = parseUUID(id)
		if err != nil {
			return err
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		parsed = append(parsed, id)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&apiKeyModel{}).Where("id = ?", uid).Update("upstream_access_mode", normalizeUpstreamAccessMode(mode))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return domain.NewError(domain.CodeNotFound, "API Key 不存在")
		}
		if err := tx.Where("api_key_id = ?", uid).Delete(&apiKeyUpstreamAccessModel{}).Error; err != nil {
			return err
		}
		if normalizeUpstreamAccessMode(mode) != "selected" || len(parsed) == 0 {
			return nil
		}
		rows := make([]apiKeyUpstreamAccessModel, 0, len(parsed))
		for _, upstreamID := range parsed {
			rows = append(rows, apiKeyUpstreamAccessModel{APIKeyID: uid, UpstreamID: upstreamID})
		}
		if err := tx.Create(&rows).Error; err != nil {
			return classifyWrite(err, "上游权限配置冲突", "包含不存在的 API Key 或上游")
		}
		return nil
	})
}
