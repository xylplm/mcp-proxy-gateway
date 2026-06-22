package store

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// 审计事件类型常量（对应 audit_log.event_type，Req 22.1/22.2/22.3）。
const (
	// AuditEventLogin 表示管理员登录事件（含成功/失败结果）。
	AuditEventLogin = "login"
	// AuditEventCreate 表示创建上游/规则/API Key 等资源。
	AuditEventCreate = "create"
	// AuditEventUpdate 表示更新上游/规则/API Key 等资源。
	AuditEventUpdate = "update"
	// AuditEventDelete 表示删除上游/规则/API Key 等资源。
	AuditEventDelete = "delete"
	// AuditEventAccessDenied 表示因鉴权失败被拒绝的访问尝试。
	AuditEventAccessDenied = "access_denied"
)

// AuditRecord 是一条审计日志（audit_log 表）（Req 22）。
type AuditRecord struct {
	// ID 为审计记录自增标识；写入前为 0，由数据库生成。
	ID int64
	// EventType 为事件类型（见 AuditEvent* 常量）。
	EventType string
	// Target 为操作目标对象（如上游名称、规则标识）；可为空。
	Target string
	// Detail 为事件明细的结构化 JSON（如登录结果、变更字段）；可为空。
	Detail json.RawMessage
	// OccurredAt 为事件发生时间；写入时为零值则由数据库默认值填充。
	OccurredAt time.Time
}

// AuditQuery describes optional filters for audit log listing.
type AuditQuery struct {
	EventType string
	Start     time.Time
	End       time.Time
}

// AuditRepo 提供审计日志的写入、倒序分页查询与保留期清理。
type AuditRepo struct {
	db *gorm.DB
}

// NewAuditRepo 构造审计日志仓储。
func NewAuditRepo(db *gorm.DB) *AuditRepo {
	return &AuditRepo{db: db}
}

// Insert 写入一条审计日志并回填生成标识与发生时间（Req 22.1、22.2、22.3）。
//
// OccurredAt 为零值时使用数据库默认值 now()，否则使用调用方提供的时间。
func (r *AuditRepo) Insert(ctx context.Context, rec AuditRecord) (AuditRecord, error) {
	var detail any
	if len(rec.Detail) > 0 {
		detail = JSONB(rec.Detail)
	}
	if rec.OccurredAt.IsZero() {
		const q = `
			INSERT INTO audit_log (event_type, target, detail)
			VALUES (?, ?, ?)
			RETURNING id, event_type, target, detail, occurred_at`
		var model auditLogModel
		if err := r.db.WithContext(ctx).Raw(q, rec.EventType, nullableString(rec.Target), detail).Scan(&model).Error; err != nil {
			return AuditRecord{}, err
		}
		return modelToAuditRecord(model), nil
	}

	const q = `
		INSERT INTO audit_log (event_type, target, detail, occurred_at)
		VALUES (?, ?, ?, ?)
		RETURNING id, event_type, target, detail, occurred_at`
	var model auditLogModel
	if err := r.db.WithContext(ctx).Raw(q, rec.EventType, nullableString(rec.Target), detail, rec.OccurredAt).Scan(&model).Error; err != nil {
		return AuditRecord{}, err
	}
	return modelToAuditRecord(model), nil
}

// List 按发生时间倒序分页返回审计记录（Req 22.4）。
//
// pageSize 为每页条数，page 为页码（从 1 起）。非法入参按下界归正：
// pageSize ≤ 0 取 1、page ≤ 0 取 1。无记录返回空切片而非错误。
func (r *AuditRepo) List(ctx context.Context, page, pageSize int, query AuditQuery) ([]AuditRecord, error) {
	if pageSize <= 0 {
		pageSize = 1
	}
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * pageSize

	db := r.applyAuditQuery(r.db.WithContext(ctx).Model(&auditLogModel{}), query)
	var models []auditLogModel
	if err := db.Order("occurred_at DESC").Order("id DESC").Limit(pageSize).Offset(offset).Find(&models).Error; err != nil {
		return nil, err
	}
	result := make([]AuditRecord, 0, len(models))
	for _, model := range models {
		result = append(result, modelToAuditRecord(model))
	}
	return result, nil
}

// Count 返回审计记录总数，供分页计算总页数使用。
func (r *AuditRepo) Count(ctx context.Context, query AuditQuery) (int64, error) {
	var n int64
	db := r.applyAuditQuery(r.db.WithContext(ctx).Model(&auditLogModel{}), query)
	if err := db.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

func (r *AuditRepo) applyAuditQuery(db *gorm.DB, query AuditQuery) *gorm.DB {
	if query.EventType != "" {
		db = db.Where("event_type = ?", query.EventType)
	}
	if !query.Start.IsZero() {
		db = db.Where("occurred_at >= ?", query.Start)
	}
	if !query.End.IsZero() {
		db = db.Where("occurred_at <= ?", query.End)
	}
	return db
}

// DeleteOlderThan 清理 occurred_at 早于 cutoff 的审计记录，返回删除条数（Req 22.5）。
func (r *AuditRepo) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("occurred_at < ?", cutoff).Delete(&auditLogModel{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

func modelToAuditRecord(model auditLogModel) AuditRecord {
	rec := AuditRecord{
		ID:         model.ID,
		EventType:  model.EventType,
		Target:     stringValue(model.Target),
		OccurredAt: model.OccurredAt,
	}
	if len(model.Detail) > 0 {
		rec.Detail = json.RawMessage(model.Detail)
	}
	return rec
}
