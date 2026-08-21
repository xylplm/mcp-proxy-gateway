package store

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// ToolCacheRepo 提供工具缓存持久副本（tool_cache 表）的存取（Req 6.1）。
//
// Redis 为热路径、PG 为持久兜底。该仓储以「整列表替换」为唯一写语义，
// 与 domain.Tool_Cache 的 Replace 契约一致，避免增量合并带来的不一致。
type ToolCacheRepo struct {
	db *gorm.DB
}

// NewToolCacheRepo 构造工具缓存持久副本仓储。
func NewToolCacheRepo(db *gorm.DB) *ToolCacheRepo {
	return &ToolCacheRepo{db: db}
}

// Replace 以整列表替换语义写入某上游 MCP 的工具列表与更新时间（Req 6.1）。
//
// tool_cache.upstream_id 为主键，故采用 UPSERT；nil 列表序列化为空 JSON 数组。
//   - 绑定的 upstream_id 不存在（违反外键）返回 CodeNotFound。
func (r *ToolCacheRepo) Replace(ctx context.Context, upstreamID string, tools []domain.ToolDef, updatedAt time.Time) error {
	uid, err := parseUUID(upstreamID)
	if err != nil {
		return err
	}
	if tools == nil {
		tools = []domain.ToolDef{}
	}
	payload, err := json.Marshal(tools)
	if err != nil {
		return domain.NewError(domain.CodeValidation, "工具列表序列化失败："+err.Error())
	}

	previous, _, found, err := r.Get(ctx, upstreamID)
	if err != nil {
		return err
	}
	summary := computeToolChangeSummary(previous, tools, found, updatedAt)
	model := toolCacheModel{
		UpstreamID:     uid,
		Tools:          JSONB(payload),
		UpdatedAt:      updatedAt,
		AddedCount:     summary.Added,
		RemovedCount:   summary.Removed,
		SchemaChanged:  summary.SchemaChanged,
		ChangeSyncedAt: summary.SyncedAt,
	}
	err = r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "upstream_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"tools", "updated_at", "added_count", "removed_count", "schema_changed", "change_synced_at"}),
	}).Create(&model).Error
	if err != nil {
		return classifyWrite(err, "工具缓存冲突", "绑定的上游 MCP 不存在")
	}
	return nil
}

// Get 读取某上游 MCP 持久化的工具列表及其更新时间。
//
// 返回值 found 为 false 表示该上游尚无持久缓存（缺失而非错误，Req 6.3）。
func (r *ToolCacheRepo) Get(ctx context.Context, upstreamID string) (tools []domain.ToolDef, updatedAt time.Time, found bool, err error) {
	uid, perr := parseUUID(upstreamID)
	if perr != nil {
		return nil, time.Time{}, false, perr
	}
	var model toolCacheModel
	scanErr := r.db.WithContext(ctx).Where("upstream_id = ?", uid).First(&model).Error
	if scanErr != nil {
		// 缓存缺失返回 found=false，不视为错误。
		if e := notFoundIfNoRows(scanErr, "工具缓存不存在"); e != scanErr {
			return nil, time.Time{}, false, nil
		}
		return nil, time.Time{}, false, scanErr
	}

	out := make([]domain.ToolDef, 0)
	if len(model.Tools) > 0 {
		if e := json.Unmarshal(model.Tools, &out); e != nil {
			return nil, time.Time{}, false, domain.NewError(domain.CodeValidation, "工具列表反序列化失败："+e.Error())
		}
	}
	return out, model.UpdatedAt, true, nil
}

// GetChangeSummary 读取某上游最近一次同步相对上一次缓存的轻量变化摘要。
func (r *ToolCacheRepo) GetChangeSummary(ctx context.Context, upstreamID string) (domain.ToolChangeSummary, bool, error) {
	uid, perr := parseUUID(upstreamID)
	if perr != nil {
		return domain.ToolChangeSummary{}, false, perr
	}
	var model toolCacheModel
	scanErr := r.db.WithContext(ctx).Where("upstream_id = ?", uid).First(&model).Error
	if scanErr != nil {
		if e := notFoundIfNoRows(scanErr, "工具缓存不存在"); e != scanErr {
			return domain.ToolChangeSummary{}, false, nil
		}
		return domain.ToolChangeSummary{}, false, scanErr
	}
	return domain.ToolChangeSummary{
		Added:         model.AddedCount,
		Removed:       model.RemovedCount,
		SchemaChanged: model.SchemaChanged,
		SyncedAt:      model.ChangeSyncedAt,
	}, !model.ChangeSyncedAt.IsZero(), nil
}

// Delete 删除某上游 MCP 的持久缓存（Req 6.6）。
//
// 删除不存在的记录不视为错误（删除语义幂等），返回是否实际删除。
func (r *ToolCacheRepo) Delete(ctx context.Context, upstreamID string) (bool, error) {
	uid, err := parseUUID(upstreamID)
	if err != nil {
		return false, err
	}
	res := r.db.WithContext(ctx).Where("upstream_id = ?", uid).Delete(&toolCacheModel{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func computeToolChangeSummary(previous, current []domain.ToolDef, found bool, syncedAt time.Time) domain.ToolChangeSummary {
	if !found {
		return domain.ToolChangeSummary{Added: len(current), SyncedAt: syncedAt}
	}
	prev := toolSchemaByOriginalName(previous)
	next := toolSchemaByOriginalName(current)
	summary := domain.ToolChangeSummary{SyncedAt: syncedAt}
	for name, schema := range next {
		oldSchema, ok := prev[name]
		if !ok {
			summary.Added++
			continue
		}
		if !jsonEqual(oldSchema, schema) {
			summary.SchemaChanged++
		}
	}
	for name := range prev {
		if _, ok := next[name]; !ok {
			summary.Removed++
		}
	}
	return summary
}

func toolSchemaByOriginalName(tools []domain.ToolDef) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(tools))
	for _, tool := range tools {
		name := tool.OriginalName
		if name == "" {
			name = tool.Name
		}
		if name == "" {
			continue
		}
		out[name] = tool.InputSchema
	}
	return out
}

func jsonEqual(a, b json.RawMessage) bool {
	var av any
	var bv any
	if len(a) == 0 {
		a = []byte("null")
	}
	if len(b) == 0 {
		b = []byte("null")
	}
	if err := json.Unmarshal(a, &av); err != nil {
		return string(a) == string(b)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return string(a) == string(b)
	}
	return reflect.DeepEqual(av, bv)
}
