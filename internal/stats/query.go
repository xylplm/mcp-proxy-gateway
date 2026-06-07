package stats

import (
	"context"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 工具排行返回条数约束：默认 10，可配置范围 1 至 100（Req 16.3）。
const (
	// minTopLimit 为工具排行返回条数下界。
	minTopLimit = 1
	// maxTopLimit 为工具排行返回条数上界。
	maxTopLimit = 100
	// defaultTopLimit 为工具排行返回条数默认值，用于配置缺失或越界时回退。
	defaultTopLimit = 10
)

// StatQuerier 是统计查询服务依赖的仓储窄接口（Req 16.2、16.3、16.4）。
//
// 仅声明本组件实际使用的多维度查询方法：按上游 MCP、按 API Key 的区间计数，以及按工具
// 维度的降序排行。*store.CallStatRepo 满足该接口；以接口而非具体类型依赖，便于单元测试
// 以内存 mock 替换。闭区间时间过滤与「开始晚于结束返回校验错误」由仓储层统一保证
// （Req 16.5、16.7）。
type StatQuerier interface {
	// CountByUpstream 统计 [start, end] 闭区间内各上游 MCP 的调用条数（含成功失败）。
	CountByUpstream(ctx context.Context, start, end time.Time) ([]store.DimensionCount, error)
	// CountByAPIKey 统计 [start, end] 闭区间内各 API Key 的调用条数（含成功失败）。
	CountByAPIKey(ctx context.Context, start, end time.Time) ([]store.DimensionCount, error)
	// TopTools 返回 [start, end] 闭区间内按调用次数降序排列的工具排行，至多 limit 条。
	TopTools(ctx context.Context, start, end time.Time, limit int) ([]store.ToolRank, error)
}

// ConfigProvider 是统计查询服务读取排行默认条数配置的窄接口。
//
// 仅声明本组件实际使用的方法：读取当前 YAML 配置快照（以获取 statistics.top_limit_default）。
// *config.Manager 满足该接口；以接口依赖便于在单元测试中注入固定默认条数。
type ConfigProvider interface {
	// Config 返回当前 YAML 常规配置的快照副本。
	Config() config.YAMLConfig
}

// QueryService 是统计查询服务（Statistics_Service 的查询侧）的实现：提供多维度统计与
// 工具排行查询（Req 16.2、16.3、16.4、16.5、16.6、16.7）。
//
// 设计要点：
//   - 闭区间时间过滤与「开始时间晚于结束时间返回校验错误」由仓储层统一实现，本层直接透传
//     （Req 16.5、16.7）；无记录时仓储返回空切片，本层据此返回空结果而非错误（Req 16.6）。
//   - 工具排行的返回条数在本层按「默认 10、范围 1-100」收敛后再下传仓储（Req 16.3）：
//     入参 ≤ 0 取配置默认值，越界值收敛到 [1,100]。
//   - QueryService 自身无共享可变状态，并发安全性由底层仓储与配置存储保证。
type QueryService struct {
	// repo 为调用统计多维度查询仓储。
	repo StatQuerier
	// cfg 为配置存储，提供工具排行默认条数配置。
	cfg ConfigProvider
}

// NewQueryService 构造统计查询服务。repo 与 cfg 均为必需依赖，任一为空时返回校验错误。
func NewQueryService(repo StatQuerier, cfg ConfigProvider) (*QueryService, error) {
	if repo == nil {
		return nil, domain.NewError(domain.CodeValidation, "统计查询服务初始化失败：统计仓储为空")
	}
	if cfg == nil {
		return nil, domain.NewError(domain.CodeValidation, "统计查询服务初始化失败：配置存储为空")
	}
	return &QueryService{repo: repo, cfg: cfg}, nil
}

// CountByUpstream 返回 [start, end] 闭区间内各上游 MCP 的调用条数（含成功失败）（Req 16.2、16.5）。
//
//   - 开始时间晚于结束时间时返回校验错误（Req 16.7，由仓储层校验）。
//   - 无记录时返回空切片而非错误（Req 16.6）。
func (s *QueryService) CountByUpstream(ctx context.Context, start, end time.Time) ([]store.DimensionCount, error) {
	return s.repo.CountByUpstream(ctx, start, end)
}

// CountByAPIKey 返回 [start, end] 闭区间内各 API Key 的调用条数（含成功失败）（Req 16.4、16.5）。
//
//   - 开始时间晚于结束时间时返回校验错误（Req 16.7，由仓储层校验）。
//   - 无记录时返回空切片而非错误（Req 16.6）。
func (s *QueryService) CountByAPIKey(ctx context.Context, start, end time.Time) ([]store.DimensionCount, error) {
	return s.repo.CountByAPIKey(ctx, start, end)
}

// TopTools 返回 [start, end] 闭区间内按调用次数降序排列的工具排行（Req 16.3、16.5）。
//
// 排行基于稳定标识 (upstream_id, original_name) 聚合（由仓储层保证）。返回条数按需求收敛：
//   - limit ≤ 0 时取配置默认值（statistics.top_limit_default，缺失或越界时回退默认 10）；
//   - limit 超过上界 100 收敛为 100，低于下界 1 收敛为 1。
//
// 行为约定：
//   - 开始时间晚于结束时间时返回校验错误（Req 16.7，由仓储层校验）。
//   - 无记录时返回空切片而非错误（Req 16.6）。
func (s *QueryService) TopTools(ctx context.Context, start, end time.Time, limit int) ([]store.ToolRank, error) {
	return s.repo.TopTools(ctx, start, end, s.resolveTopLimit(limit))
}

// resolveTopLimit 计算生效的工具排行返回条数（Req 16.3）。
//
//	limit ≤ 0  → 取配置默认值（越界或未配置回退默认 10）
//	limit > 100 → 收敛为 100
//	limit < 1  → 收敛为 1（与 ≤0 分支配合，覆盖全部非法下界）
func (s *QueryService) resolveTopLimit(limit int) int {
	if limit <= 0 {
		return s.defaultTopLimit()
	}
	if limit > maxTopLimit {
		return maxTopLimit
	}
	return limit
}

// defaultTopLimit 返回配置的工具排行默认条数，对越界或未配置的值回退为默认 10。
func (s *QueryService) defaultTopLimit() int {
	d := s.cfg.Config().Statistics.TopLimitDefault
	if d < minTopLimit || d > maxTopLimit {
		return defaultTopLimit
	}
	return d
}
