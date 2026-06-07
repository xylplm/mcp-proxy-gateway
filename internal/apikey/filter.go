package apikey

import (
	"context"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// FilterRepository 是 API Key 级屏蔽规则管理依赖的仓储窄接口（Req 13.1）。
//
// 仅声明本组件实际使用的方法，便于在单元测试（任务 14.3）中以内存 fake 替换，
// 同时使依赖关系一目了然。*store.FilterAPIKeyRepo 满足该接口。
type FilterRepository interface {
	// Create 持久化一条 API Key 级屏蔽规则并回填生成标识；绑定的 API Key 不存在返回 NOT_FOUND。
	Create(ctx context.Context, row store.FilterAPIKeyRow) (store.FilterAPIKeyRow, error)
	// ListByAPIKey 返回某 API Key 的全部屏蔽规则，按 sort_order 升序；无数据返回空切片。
	ListByAPIKey(ctx context.Context, apiKeyID string) ([]store.FilterAPIKeyRow, error)
	// CountByAPIKey 统计某 API Key 已有的屏蔽规则数量，供上限校验使用。
	CountByAPIKey(ctx context.Context, apiKeyID string) (int, error)
	// SetEnabled 仅更新某条屏蔽规则的启停状态；不存在返回 NOT_FOUND。
	SetEnabled(ctx context.Context, id string, enabled bool) error
	// Delete 删除一条屏蔽规则；不存在返回 NOT_FOUND。
	Delete(ctx context.Context, id string) error
}

// FilterValidator 是屏蔽规则字段级校验依赖的窄接口（Req 13.4）。
//
// 复用领域规则引擎（domain.Rule_Engine）的 ValidateFilter，避免在应用层重复实现
// 正则合法性与模式长度（1-200）校验逻辑；*domain.engine（经 domain.NewRuleEngine
// 构造）满足该接口。以接口而非具体类型依赖，便于单元测试注入真实的纯函数引擎。
type FilterValidator interface {
	// ValidateFilter 在保存前校验单条屏蔽规则（正则合法性、模式长度 1-200）。
	ValidateFilter(r domain.FilterRule) error
}

// 编译期断言：生产仓储满足本组件所需的窄接口。
var _ FilterRepository = (*store.FilterAPIKeyRepo)(nil)

// Filter 为 API Key 级屏蔽规则的对外视图，是 Create/List 的返回类型。
//
// 它在 domain.FilterRule 的字段基础上额外携带其绑定的 API Key 标识 APIKeyID，
// 与持久层行 store.FilterAPIKeyRow 一一对应，但不暴露任何存储层细节。
type Filter struct {
	// ID 为规则唯一标识。
	ID string `json:"id"`
	// APIKeyID 为该规则绑定的 API Key 标识。
	APIKeyID string `json:"apiKeyId"`
	// Pattern 为匹配模式，长度需在 1 至 200 个字符之间。
	Pattern string `json:"pattern"`
	// IsRegex 表示是否启用正则匹配（完整匹配）。
	IsRegex bool `json:"isRegex"`
	// Enabled 表示该规则是否启用，支持单条启停。
	Enabled bool `json:"enabled"`
	// SortOrder 为规则排序顺序，List 按其升序返回。
	SortOrder int `json:"sortOrder"`
}

// CreateFilterInput 为创建 API Key 级屏蔽规则的输入参数。
//
// SortOrder 不在输入内：创建时由管理器按该 API Key 当前规则数自动追加到末尾，
// 保证 List 的稳定升序，无需调用方维护排序。
type CreateFilterInput struct {
	// APIKeyID 为该规则绑定的 API Key 标识。
	APIKeyID string
	// Pattern 为匹配模式，长度需在 1 至 200 个字符之间（Req 13.1、13.4）。
	Pattern string
	// IsRegex 表示是否启用正则匹配；为 true 时 Pattern 须为合法正则（Req 13.4）。
	IsRegex bool
	// Enabled 表示创建后该规则是否处于启用状态（Req 13.1）。
	Enabled bool
}

// FilterManager 管理 API Key 级屏蔽规则的生命周期（Req 13.1、13.2、13.3、13.4、13.8）。
//
// 职责：在 API Key 上创建/启停/列出/删除屏蔽规则，并在创建时强制两类约束——
//   - 字段级校验：匹配模式长度 1-200、正则合法性（复用领域规则引擎 ValidateFilter）。
//   - 数量上限：单个 API Key 至多 100 条（复用 domain.ValidateFilterCount，在应用层强制）。
//
// 持久化的规则可经 ListByAPIKey 读取，供聚合管线第 6 阶段在 API Key 视角下过滤工具
// （匹配工具的 OriginalName，Req 13.7）；该过滤逻辑由聚合管线实现，本管理器只负责 CRUD
// 与校验，确保数据形态（store.FilterAPIKeyRow 内嵌 domain.FilterRule）可被管线直接消费。
//
// 本类型独立于 API Key 元数据管理器 Manager，方法名（Create/SetEnabled/List/Delete）
// 因接收者不同而不与 Manager 的同名方法冲突。
type FilterManager struct {
	// repo 为 API Key 级屏蔽规则仓储。
	repo FilterRepository
	// validator 为屏蔽规则字段级校验器（领域规则引擎）。
	validator FilterValidator
}

// NewFilterManager 构造 API Key 级屏蔽规则管理器。repo 与 validator 均为必需依赖。
func NewFilterManager(repo FilterRepository, validator FilterValidator) *FilterManager {
	return &FilterManager{repo: repo, validator: validator}
}

// Create 在某个 API Key 上创建一条屏蔽规则（Req 13.1、13.2、13.3、13.4）。
//
// 流程（任一校验失败均不持久化任何数据）：
//  1. 字段级校验：复用规则引擎 ValidateFilter 校验匹配模式长度（1-200）与正则合法性；
//     不通过返回字段级 VALIDATION 错误（Fields 含 pattern，Req 13.4）。
//  2. 数量上限校验：读取该 API Key 当前规则数，复用 domain.ValidateFilterCount 判定；
//     已达 100 条上限时返回 VALIDATION 错误并拒绝（Req 13.2、13.3）。
//  3. 持久化：以当前规则数作为 SortOrder 将新规则追加到末尾，绑定到该 API Key。
//
// 错误语义：
//   - 匹配模式无效（空/超 200/非法正则）：返回 VALIDATION（Req 13.4）。
//   - 规则数量将超过上限：返回 VALIDATION（Req 13.3）。
//   - 绑定的 API Key 不存在：仓储层返回 NOT_FOUND，原样透传。
func (m *FilterManager) Create(ctx context.Context, in CreateFilterInput) (Filter, error) {
	rule := domain.FilterRule{
		Pattern: in.Pattern,
		IsRegex: in.IsRegex,
		Enabled: in.Enabled,
	}

	// 1. 字段级校验（正则合法性、模式长度 1-200），不通过即拒绝且不持久化（Req 13.4）。
	if err := m.validator.ValidateFilter(rule); err != nil {
		return Filter{}, err
	}

	// 2. 数量上限校验：单个 API Key 至多 100 条（Req 13.2、13.3）。
	current, err := m.repo.CountByAPIKey(ctx, in.APIKeyID)
	if err != nil {
		return Filter{}, err
	}
	if err := domain.ValidateFilterCount(current); err != nil {
		return Filter{}, err
	}

	// 3. 持久化：以当前规则数作为排序值追加到末尾，保证 List 的稳定升序。
	rule.SortOrder = current
	row, err := m.repo.Create(ctx, store.FilterAPIKeyRow{
		FilterRule: rule,
		APIKeyID:   in.APIKeyID,
	})
	if err != nil {
		return Filter{}, err
	}
	return toFilter(row), nil
}

// SetEnabled 启用或停用某条 API Key 级屏蔽规则（Req 13.8）。
//
// 启停状态的更新即时生效：聚合管线在该次更新之后接收的请求会按新的启用状态匹配
// （停用规则在匹配中被忽略）。标识不存在返回 NOT_FOUND。
func (m *FilterManager) SetEnabled(ctx context.Context, id string, enabled bool) error {
	return m.repo.SetEnabled(ctx, id, enabled)
}

// List 返回某 API Key 的全部屏蔽规则，按 SortOrder 升序；无规则返回空切片而非错误。
//
// 返回的规则集合即聚合管线第 6 阶段在该 API Key 视角下用于过滤的输入（Req 13.7）。
func (m *FilterManager) List(ctx context.Context, apiKeyID string) ([]Filter, error) {
	rows, err := m.repo.ListByAPIKey(ctx, apiKeyID)
	if err != nil {
		return nil, err
	}
	out := make([]Filter, 0, len(rows))
	for i := range rows {
		out = append(out, toFilter(rows[i]))
	}
	return out, nil
}

// Delete 删除某条 API Key 级屏蔽规则；标识不存在返回 NOT_FOUND。
func (m *FilterManager) Delete(ctx context.Context, id string) error {
	return m.repo.Delete(ctx, id)
}

// toFilter 将持久层行映射为对外视图。
func toFilter(row store.FilterAPIKeyRow) Filter {
	return Filter{
		ID:        row.ID,
		APIKeyID:  row.APIKeyID,
		Pattern:   row.Pattern,
		IsRegex:   row.IsRegex,
		Enabled:   row.Enabled,
		SortOrder: row.SortOrder,
	}
}
