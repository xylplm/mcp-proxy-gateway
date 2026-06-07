package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
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

// AuditRepo 提供审计日志的写入、倒序分页查询与保留期清理。
type AuditRepo struct {
	pool *pgxpool.Pool
}

// NewAuditRepo 构造审计日志仓储。
func NewAuditRepo(pool *pgxpool.Pool) *AuditRepo {
	return &AuditRepo{pool: pool}
}

// Insert 写入一条审计日志并回填生成标识与发生时间（Req 22.1/22.2/22.3）。
//
// OccurredAt 为零值时使用数据库默认值 now()，否则使用调用方提供的时间。
func (r *AuditRepo) Insert(ctx context.Context, rec AuditRecord) (AuditRecord, error) {
	var detail []byte
	if len(rec.Detail) > 0 {
		detail = rec.Detail
	}

	if rec.OccurredAt.IsZero() {
		const q = `
			INSERT INTO audit_log (event_type, target, detail)
			VALUES ($1, $2, $3)
			RETURNING id, occurred_at`
		err := r.pool.QueryRow(ctx, q, rec.EventType, nullableText(rec.Target), detail).
			Scan(&rec.ID, &rec.OccurredAt)
		if err != nil {
			return AuditRecord{}, err
		}
		return rec, nil
	}

	const q = `
		INSERT INTO audit_log (event_type, target, detail, occurred_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, occurred_at`
	err := r.pool.QueryRow(ctx, q, rec.EventType, nullableText(rec.Target), detail, rec.OccurredAt).
		Scan(&rec.ID, &rec.OccurredAt)
	if err != nil {
		return AuditRecord{}, err
	}
	return rec, nil
}

// List 按发生时间倒序分页返回审计记录（Req 22.4）。
//
// pageSize 为每页条数，page 为页码（从 1 起）。非法入参按下界归正：
// pageSize ≤ 0 取 1、page ≤ 0 取 1。无记录返回空切片而非错误。
func (r *AuditRepo) List(ctx context.Context, page, pageSize int) ([]AuditRecord, error) {
	if pageSize <= 0 {
		pageSize = 1
	}
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * pageSize

	const q = `
		SELECT id, event_type, target, detail, occurred_at
		FROM audit_log
		ORDER BY occurred_at DESC, id DESC
		LIMIT $1 OFFSET $2`
	rows, err := r.pool.Query(ctx, q, pageSize, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]AuditRecord, 0)
	for rows.Next() {
		var (
			id         int64
			eventType  string
			target     pgtype.Text
			detail     []byte
			occurredAt time.Time
		)
		if err := rows.Scan(&id, &eventType, &target, &detail, &occurredAt); err != nil {
			return nil, err
		}
		rec := AuditRecord{
			ID:         id,
			EventType:  eventType,
			Target:     target.String,
			OccurredAt: occurredAt,
		}
		if len(detail) > 0 {
			rec.Detail = json.RawMessage(detail)
		}
		result = append(result, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// Count 返回审计记录总数，供分页计算总页数使用。
func (r *AuditRepo) Count(ctx context.Context) (int64, error) {
	const q = `SELECT count(*) FROM audit_log`
	var n int64
	if err := r.pool.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// DeleteOlderThan 清理 occurred_at 早于 cutoff 的审计记录，返回删除条数（Req 22.5）。
func (r *AuditRepo) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	const q = `DELETE FROM audit_log WHERE occurred_at < $1`
	tag, err := r.pool.Exec(ctx, q, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
