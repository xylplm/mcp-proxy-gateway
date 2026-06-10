package audit

import (
	"context"

	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 审计分页约束：默认每页 20 条，可配置范围 1 至 200（Req 22.4）。
const (
	// minPageSize 为每页条数下界。
	minPageSize = 1
	// maxPageSize 为每页条数上界。
	maxPageSize = 200
	// defaultPageSize 为每页条数默认值，用于配置缺失或越界时回退。
	defaultPageSize = 20
)

// PageResult 是审计日志分页查询结果（Req 22.4）。
//
// Records 为本页记录，按发生时间倒序排列；无数据时为空切片而非 nil。
// Page 与 PageSize 为实际生效的分页参数（已对非法入参收敛），便于调用方回显与计算总页数。
type PageResult struct {
	// Records 为本页审计记录，按 occurred_at 倒序；空结果时为空切片。
	Records []store.AuditRecord
	// Page 为生效页码（从 1 起）。
	Page int
	// PageSize 为生效的每页条数（已收敛到 1 至 200）。
	PageSize int
	// Total 为审计记录总数，供调用方计算总页数。
	Total int64
}

// Query describes optional audit log filters.
type Query = store.AuditQuery

// List 按发生时间倒序分页返回审计记录及总数（Req 22.4）。
//
// 入参收敛规则：
//   - page ≤ 0 归正为第 1 页；
//   - pageSize ≤ 0 取配置默认值（audit.page_size_default，缺失或越界时回退默认 20）；
//   - pageSize 超过上界 200 收敛为 200。
//
// 倒序与偏移由底层仓储保证；无记录时 Records 为空切片。Count 与 List 任一失败均透传错误。
func (s *Service) List(ctx context.Context, page, pageSize int, query Query) (PageResult, error) {
	if page <= 0 {
		page = 1
	}
	size := s.resolvePageSize(pageSize)

	total, err := s.repo.Count(ctx, query)
	if err != nil {
		return PageResult{}, err
	}
	records, err := s.repo.List(ctx, page, size, query)
	if err != nil {
		return PageResult{}, err
	}
	return PageResult{
		Records:  records,
		Page:     page,
		PageSize: size,
		Total:    total,
	}, nil
}

// resolvePageSize 计算生效的每页条数：≤0 取配置默认值，超过上界 200 收敛为 200。
func (s *Service) resolvePageSize(pageSize int) int {
	if pageSize <= 0 {
		return s.defaultPageSize()
	}
	if pageSize > maxPageSize {
		return maxPageSize
	}
	return pageSize
}

// defaultPageSize 返回配置的默认每页条数，对越界或未配置的值回退为默认 20。
func (s *Service) defaultPageSize() int {
	d := s.cfg.Config().Audit.PageSizeDefault
	if d < minPageSize || d > maxPageSize {
		return defaultPageSize
	}
	return d
}
