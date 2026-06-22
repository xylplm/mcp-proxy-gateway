package store

import (
	"context"
	"encoding/json"

	"github.com/lib/pq"
	"gorm.io/gorm"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// UpstreamRow 是上游 MCP 在持久层的完整行表示。
//
// 凭证明文直接承载于 domain.Upstream.Config.Credential，仓储层只负责存取明文，
// 不再做加解密。State 与 LastError 为运行期状态，由连接管理器维护，读取时为零值。
type UpstreamRow struct {
	domain.Upstream
}

// UpstreamRepo 提供上游 MCP 服务（upstream_mcp 表）的类型安全增删查改。
type UpstreamRepo struct {
	db *gorm.DB
}

// NewUpstreamRepo 构造上游 MCP 仓储。
func NewUpstreamRepo(db *gorm.DB) *UpstreamRepo {
	return &UpstreamRepo{db: db}
}

// Create 持久化一条上游 MCP 配置并返回含生成标识与时间戳的记录。
//   - 名称与其他上游重复（违反 UNIQUE）返回 CodeConflict（Req 2.7）。
//   - 连接参数以 JSONB 存储；credential 为凭证明文（可为空字符串）。
func (r *UpstreamRepo) Create(ctx context.Context, cfg domain.UpstreamConfig) (*UpstreamRow, error) {
	connParams, err := marshalConnParams(cfg.ConnParams)
	if err != nil {
		return nil, err
	}
	rateLimits, err := marshalRateLimits(cfg.RateLimits)
	if err != nil {
		return nil, err
	}

	id := newUUID()
	values := map[string]any{
		"id":          id,
		"name":        cfg.Name,
		"tags":        pq.StringArray(storeTags(cfg.Tags)),
		"transport":   string(cfg.Transport),
		"conn_params": JSONB(connParams),
		"credential":  cfg.Credential,
		"enabled":     cfg.Enabled,
		"sort_order":  cfg.SortOrder,
		"auto_sync":   cfg.AutoSync,
		"rate_limits": JSONB(rateLimits),
	}
	if err := r.db.WithContext(ctx).Model(&upstreamMCPModel{}).Create(values).Error; err != nil {
		return nil, classifyWrite(err, "上游 MCP 名称已存在："+cfg.Name, "上游 MCP 不存在")
	}
	return r.Get(ctx, id)
}

// Get 按标识查询单条上游 MCP；不存在返回 CodeNotFound（Req 2.6）。
func (r *UpstreamRepo) Get(ctx context.Context, id string) (*UpstreamRow, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	var model upstreamMCPModel
	if err := r.db.WithContext(ctx).Where("id = ?", uid).First(&model).Error; err != nil {
		return nil, notFoundIfNoRows(err, "上游 MCP 不存在")
	}
	return modelToUpstream(model)
}

// List 返回全部上游 MCP，按 sort_order 升序、创建时间次序排列；无数据返回空切片（Req 2.8、3.4）。
func (r *UpstreamRepo) List(ctx context.Context) ([]UpstreamRow, error) {
	var models []upstreamMCPModel
	if err := r.db.WithContext(ctx).Order("sort_order ASC").Order("created_at ASC").Find(&models).Error; err != nil {
		return nil, err
	}
	result := make([]UpstreamRow, 0, len(models))
	for _, model := range models {
		row, err := modelToUpstream(model)
		if err != nil {
			return nil, err
		}
		result = append(result, *row)
	}
	return result, nil
}

// Update 更新指定上游 MCP 的配置（含 credential 明文）并刷新 updated_at。
//   - 标识不存在返回 CodeNotFound（Req 2.6）。
//   - 名称与其他上游重复返回 CodeConflict（Req 2.7）。
func (r *UpstreamRepo) Update(ctx context.Context, id string, cfg domain.UpstreamConfig) (*UpstreamRow, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	connParams, err := marshalConnParams(cfg.ConnParams)
	if err != nil {
		return nil, err
	}
	rateLimits, err := marshalRateLimits(cfg.RateLimits)
	if err != nil {
		return nil, err
	}

	updates := map[string]any{
		"name":        cfg.Name,
		"tags":        pq.StringArray(storeTags(cfg.Tags)),
		"transport":   string(cfg.Transport),
		"conn_params": JSONB(connParams),
		"credential":  cfg.Credential,
		"enabled":     cfg.Enabled,
		"sort_order":  cfg.SortOrder,
		"auto_sync":   cfg.AutoSync,
		"rate_limits": JSONB(rateLimits),
		"updated_at":  gorm.Expr("now()"),
	}
	res := r.db.WithContext(ctx).Model(&upstreamMCPModel{}).Where("id = ?", uid).Updates(updates)
	if res.Error != nil {
		return nil, classifyWrite(res.Error, "上游 MCP 名称已存在："+cfg.Name, "上游 MCP 不存在")
	}
	if res.RowsAffected == 0 {
		return nil, domain.NewError(domain.CodeNotFound, "上游 MCP 不存在")
	}
	return r.Get(ctx, id)
}

// SetEnabled 仅更新启停状态（Req 3.1、3.2）；标识不存在返回 CodeNotFound。
func (r *UpstreamRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	res := r.db.WithContext(ctx).Model(&upstreamMCPModel{}).
		Where("id = ?", uid).
		Updates(map[string]any{"enabled": enabled, "updated_at": gorm.Expr("now()")})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.NewError(domain.CodeNotFound, "上游 MCP 不存在")
	}
	return nil
}

// SetSortOrder 仅更新单个上游的排序值，供排序持久化复用（Req 3.4）。
func (r *UpstreamRepo) SetSortOrder(ctx context.Context, id string, sortOrder int) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	res := r.db.WithContext(ctx).Model(&upstreamMCPModel{}).
		Where("id = ?", uid).
		Updates(map[string]any{"sort_order": sortOrder, "updated_at": gorm.Expr("now()")})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.NewError(domain.CodeNotFound, "上游 MCP 不存在")
	}
	return nil
}

// Delete 删除指定上游 MCP；其从属规则、ACL 与工具缓存通过外键 ON DELETE CASCADE 级联清理（Req 2.5、6.6）。
//   - 标识不存在返回 CodeNotFound（Req 2.6）。
func (r *UpstreamRepo) Delete(ctx context.Context, id string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	res := r.db.WithContext(ctx).Where("id = ?", uid).Delete(&upstreamMCPModel{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.NewError(domain.CodeNotFound, "上游 MCP 不存在")
	}
	return nil
}

// marshalConnParams 将连接参数序列化为 JSONB 字节；nil map 序列化为 "{}"。
func marshalConnParams(params map[string]any) ([]byte, error) {
	if params == nil {
		params = map[string]any{}
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil, domain.NewError(domain.CodeValidation, "连接参数序列化失败："+err.Error())
	}
	return b, nil
}

func marshalRateLimits(limits domain.UpstreamRateLimits) ([]byte, error) {
	b, err := json.Marshal(limits)
	if err != nil {
		return nil, domain.NewError(domain.CodeValidation, "限流配置序列化失败："+err.Error())
	}
	return b, nil
}

func storeTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

func modelToUpstream(model upstreamMCPModel) (*UpstreamRow, error) {
	params := map[string]any{}
	if len(model.ConnParams) > 0 {
		if err := json.Unmarshal(model.ConnParams, &params); err != nil {
			return nil, domain.NewError(domain.CodeValidation, "连接参数反序列化失败："+err.Error())
		}
	}
	var rateLimits domain.UpstreamRateLimits
	if len(model.RateLimits) > 0 {
		if err := json.Unmarshal(model.RateLimits, &rateLimits); err != nil {
			return nil, domain.NewError(domain.CodeValidation, "限流配置反序列化失败："+err.Error())
		}
	}

	out := &UpstreamRow{}
	out.ID = model.ID
	out.Config = domain.UpstreamConfig{
		Name:       model.Name,
		Tags:       []string(model.Tags),
		Transport:  domain.TransportType(model.Transport),
		ConnParams: params,
		Credential: model.Credential,
		Enabled:    model.Enabled,
		SortOrder:  model.SortOrder,
		AutoSync:   model.AutoSync,
		RateLimits: rateLimits,
	}
	out.CreatedAt = model.CreatedAt
	out.UpdatedAt = model.UpdatedAt
	return out, nil
}
