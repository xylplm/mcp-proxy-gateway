package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 审计保留期约束：默认 180 天，可配置范围 1 至 3650 天（Req 22.5）。
const (
	// minRetentionDays 为审计日志保留天数下界。
	minRetentionDays = 1
	// maxRetentionDays 为审计日志保留天数上界（约 10 年）。
	maxRetentionDays = 3650
	// defaultRetentionDays 为审计日志保留天数默认值。
	defaultRetentionDays = 180
)

// ResourceKind 为受审计的管理资源类别（Req 22.2）。
//
// 用于在配置变更事件中标注被增改删的目标资源类型，便于审计检索时按资源维度过滤。
type ResourceKind string

const (
	// ResourceUpstream 表示上游 MCP 服务。
	ResourceUpstream ResourceKind = "upstream"
	// ResourceRule 表示别名/屏蔽规则。
	ResourceRule ResourceKind = "rule"
	// ResourceAPIKey 表示 API Key。
	ResourceAPIKey ResourceKind = "api_key"
	// ResourceAdmin 表示管理员账号（注册/改密）。
	ResourceAdmin ResourceKind = "admin"
	// ResourceSetting 表示系统设置。
	ResourceSetting ResourceKind = "setting"
	// ResourceScript 表示脚本中心受管脚本资产。
	ResourceScript ResourceKind = "script"
)

// AuditRepository 是审计服务依赖的仓储窄接口（Req 22）。
//
// 仅声明本组件实际使用的方法：写入一条审计记录、按发生时间倒序分页查询、统计总数、
// 按截止时间清理超期记录。*store.AuditRepo 满足该接口；以接口而非具体类型依赖，
// 便于单元测试以内存 mock 替换。分页查询逻辑见 query.go。
type AuditRepository interface {
	// Insert 写入一条审计日志并回填生成标识与发生时间。
	Insert(ctx context.Context, rec store.AuditRecord) (store.AuditRecord, error)
	// List 按发生时间倒序分页返回审计记录（page 从 1 起，pageSize 为每页条数）。
	List(ctx context.Context, page, pageSize int, query Query) ([]store.AuditRecord, error)
	// Count 返回审计记录总数，供分页计算总页数使用。
	Count(ctx context.Context, query Query) (int64, error)
	// DeleteOlderThan 清理发生时间早于 cutoff 的审计记录，返回删除条数。
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// ConfigProvider 是审计服务读取保留期配置的窄接口。
//
// 仅声明本组件实际使用的方法：读取当前 YAML 配置快照（以获取 audit.retention_days）。
// *config.Manager 满足该接口；以接口依赖便于在单元测试中注入固定保留期。
type ConfigProvider interface {
	// Config 返回当前 YAML 常规配置的快照副本。
	Config() config.YAMLConfig
}

// Service 是审计日志服务（Audit_Service）的实现：记录登录、配置变更与被拒访问，
// 并按配置的保留期清理超期记录（Req 22.1、22.2、22.3、22.5）。
//
// 本任务（18.1）聚焦事件记录与保留期清理，对外提供可被认证与管理操作路径调用的
// 记录方法（实际接线在任务 19.x/27.2 完成）；倒序分页查询在任务 18.2 实现。
//
// 所有记录方法显式以注入时钟标注发生时间戳，既满足"记录时间戳"的需求，又使行为可测；
// Service 自身无共享可变状态，其并发安全性由底层仓储与配置存储保证。
type Service struct {
	// repo 为审计日志仓储。
	repo AuditRepository
	// cfg 为配置存储，提供审计保留期配置。
	cfg ConfigProvider
	// now 返回当前时间，便于在测试中注入可控时钟。
	now func() time.Time
}

// New 构造审计日志服务。repo 与 cfg 均为必需依赖，任一为空时返回 VALIDATION 错误。
func New(repo AuditRepository, cfg ConfigProvider) (*Service, error) {
	if repo == nil {
		return nil, domain.NewError(domain.CodeValidation, "审计服务初始化失败：审计仓储为空")
	}
	if cfg == nil {
		return nil, domain.NewError(domain.CodeValidation, "审计服务初始化失败：配置存储为空")
	}
	return &Service{
		repo: repo,
		cfg:  cfg,
		now:  time.Now,
	}, nil
}

// RecordLogin 记录一次管理员登录事件及其结果与时间戳（Req 22.1）。
//
// target 为登录所用用户名；success 表示该次登录是否成功（凭证匹配）。
// 事件类型固定为 login，结果写入明细 JSON，发生时间取注入时钟当前时刻。
func (s *Service) RecordLogin(ctx context.Context, username string, success bool) error {
	return s.record(ctx, store.AuditEventLogin, username, map[string]any{
		"success": success,
	})
}

// RecordCreate 记录一次资源创建事件（Req 22.2）。
//
// kind 为被创建资源的类别（上游/规则/API Key），target 为目标对象标识（如名称或 ID）。
func (s *Service) RecordCreate(ctx context.Context, kind ResourceKind, target string) error {
	return s.recordChange(ctx, store.AuditEventCreate, kind, target)
}

// RecordUpdate 记录一次资源更新事件（Req 22.2）。
//
// kind 为被更新资源的类别，target 为目标对象标识。
func (s *Service) RecordUpdate(ctx context.Context, kind ResourceKind, target string) error {
	return s.recordChange(ctx, store.AuditEventUpdate, kind, target)
}

// RecordDelete 记录一次资源删除事件（Req 22.2）。
//
// kind 为被删除资源的类别，target 为目标对象标识。
func (s *Service) RecordDelete(ctx context.Context, kind ResourceKind, target string) error {
	return s.recordChange(ctx, store.AuditEventDelete, kind, target)
}

// RecordAccessDenied 记录一次因鉴权失败被拒绝的访问尝试（Req 22.3）。
//
// target 为被尝试访问的目标（如请求路径或资源标识）；reason 为拒绝原因（可为空），
// 非空时写入明细 JSON。事件类型固定为 access_denied。
func (s *Service) RecordAccessDenied(ctx context.Context, target, reason string) error {
	var detail map[string]any
	if reason != "" {
		detail = map[string]any{"reason": reason}
	}
	return s.record(ctx, store.AuditEventAccessDenied, target, detail)
}

// Cleanup 按配置的保留期清理超期审计记录，返回被删除的条数（Req 22.5）。
//
// 截止时间为"当前时刻减去保留天数"，发生时间早于该时刻的记录被删除。保留天数取自
// 配置 audit.retention_days；越界或缺失时回退为默认 180 天，确保清理边界始终合法。
// 该方法供定时任务（接线在任务 27.2）周期性调用。
func (s *Service) Cleanup(ctx context.Context) (int64, error) {
	cutoff := s.now().AddDate(0, 0, -s.retentionDays())
	return s.repo.DeleteOlderThan(ctx, cutoff)
}

// retentionDays 返回生效的审计保留天数，对越界或未配置的值回退为默认 180 天。
func (s *Service) retentionDays() int {
	d := s.cfg.Config().Audit.RetentionDays
	if d < minRetentionDays || d > maxRetentionDays {
		return defaultRetentionDays
	}
	return d
}

// recordChange 记录一次资源增改删事件，将资源类别写入明细以便按维度检索（Req 22.2）。
func (s *Service) recordChange(ctx context.Context, eventType string, kind ResourceKind, target string) error {
	return s.record(ctx, eventType, target, map[string]any{
		"resource": string(kind),
	})
}

// record 组装并写入一条审计记录：标注事件类型、目标、明细与发生时间戳。
//
// detail 为 nil 时不写入明细（对应数据库 detail 列为 NULL）；否则序列化为 JSON。
// 发生时间显式取注入时钟，保证"记录时间戳"语义明确且行为可测（Req 22.1、22.2、22.3）。
// 组装细节委派给 buildRecord（与异步 Recorder 共用，避免明细序列化逻辑重复）。
func (s *Service) record(ctx context.Context, eventType, target string, detail map[string]any) error {
	rec, err := buildRecord(eventType, target, detail)
	if err != nil {
		return err
	}
	rec.OccurredAt = s.now()
	if _, err := s.repo.Insert(ctx, rec); err != nil {
		return err
	}
	return nil
}

// buildRecord 组装一条审计记录的事件类型、目标与明细 JSON，但不设置发生时间戳（Req 22）。
//
// 抽出为未导出纯函数，供 Service 同步写与 Recorder 异步写共用，确保明细序列化逻辑唯一。
// detail 为 nil 时不写入明细（对应数据库 detail 列为 NULL）；否则序列化为 JSON。
// OccurredAt 由调用方按各自时钟设置：同步写取 Service.now()，异步写取提交时刻（见 Recorder）。
func buildRecord(eventType, target string, detail map[string]any) (store.AuditRecord, error) {
	rec := store.AuditRecord{
		EventType: eventType,
		Target:    target,
	}
	if detail != nil {
		raw, err := json.Marshal(detail)
		if err != nil {
			return store.AuditRecord{}, domain.NewError(domain.CodeValidation, "序列化审计明细失败："+err.Error())
		}
		rec.Detail = raw
	}
	return rec, nil
}
