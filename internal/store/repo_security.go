package store

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

const (
	SecurityBlockStatusActive   = "active"
	SecurityBlockStatusReleased = "released"
	SecurityBlockStatusExpired  = "expired"
)

type SecurityEvent struct {
	ID             int64
	EventType      string
	SubjectType    string
	Subject        string
	ClientIP       string
	APIKeyID       string
	APIKeyPrefix   string
	KeyFingerprint string
	Method         string
	Path           string
	UserAgent      string
	Reason         string
	Count          int
	CreatedAt      time.Time
}

type SecurityBlock struct {
	ID             string
	SubjectType    string
	Subject        string
	ClientIP       string
	APIKeyID       string
	APIKeyPrefix   string
	KeyFingerprint string
	Reason         string
	FailureCount   int
	Status         string
	BlockedUntil   *time.Time
	ReleasedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type SecurityEventQuery struct {
	EventType   string
	ClientIP    string
	APIKeyID    string
	SubjectType string
	Limit       int
}

type SecurityBlockQuery struct {
	Status string
	Limit  int
}

type SecuritySummary struct {
	ActiveBlocks        int64
	AuthFailures24h     int64
	ACLDenies24h        int64
	HighRiskSubjects24h int64
}

type SecurityRepo struct {
	db *gorm.DB
}

func NewSecurityRepo(db *gorm.DB) *SecurityRepo {
	return &SecurityRepo{db: db}
}

func (r *SecurityRepo) InsertEvent(ctx context.Context, ev SecurityEvent) (SecurityEvent, error) {
	now := ev.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	model := securityEventModel{
		EventType:      ev.EventType,
		SubjectType:    ev.SubjectType,
		Subject:        ev.Subject,
		ClientIP:       ev.ClientIP,
		APIKeyID:       ev.APIKeyID,
		APIKeyPrefix:   ev.APIKeyPrefix,
		KeyFingerprint: ev.KeyFingerprint,
		Method:         ev.Method,
		Path:           ev.Path,
		UserAgent:      ev.UserAgent,
		Reason:         ev.Reason,
		Count:          ev.Count,
		CreatedAt:      now,
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return SecurityEvent{}, err
	}
	return modelToSecurityEvent(model), nil
}

func (r *SecurityRepo) CreateBlock(ctx context.Context, block SecurityBlock) (SecurityBlock, error) {
	if block.ID == "" {
		block.ID = newUUID()
	}
	if block.Status == "" {
		block.Status = SecurityBlockStatusActive
	}
	now := time.Now()
	model := securityBlockModel{
		ID:             block.ID,
		SubjectType:    block.SubjectType,
		Subject:        block.Subject,
		ClientIP:       block.ClientIP,
		APIKeyID:       block.APIKeyID,
		APIKeyPrefix:   block.APIKeyPrefix,
		KeyFingerprint: block.KeyFingerprint,
		Reason:         block.Reason,
		FailureCount:   block.FailureCount,
		Status:         block.Status,
		BlockedUntil:   block.BlockedUntil,
		ReleasedAt:     block.ReleasedAt,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return SecurityBlock{}, err
	}
	return modelToSecurityBlock(model), nil
}

func (r *SecurityRepo) ListEvents(ctx context.Context, query SecurityEventQuery) ([]SecurityEvent, error) {
	limit := clampSecurityLimit(query.Limit)
	db := r.db.WithContext(ctx).Model(&securityEventModel{})
	if query.EventType != "" {
		db = db.Where("event_type = ?", query.EventType)
	}
	if query.ClientIP != "" {
		db = db.Where("client_ip = ?", query.ClientIP)
	}
	if query.APIKeyID != "" {
		db = db.Where("api_key_id = ?", query.APIKeyID)
	}
	if query.SubjectType != "" {
		db = db.Where("subject_type = ?", query.SubjectType)
	}
	var models []securityEventModel
	if err := db.Order("created_at DESC").Order("id DESC").Limit(limit).Find(&models).Error; err != nil {
		return nil, err
	}
	out := make([]SecurityEvent, 0, len(models))
	for _, model := range models {
		out = append(out, modelToSecurityEvent(model))
	}
	return out, nil
}

func (r *SecurityRepo) ListBlocks(ctx context.Context, query SecurityBlockQuery) ([]SecurityBlock, error) {
	limit := clampSecurityLimit(query.Limit)
	db := r.db.WithContext(ctx).Model(&securityBlockModel{})
	if query.Status != "" {
		db = db.Where("status = ?", query.Status)
	}
	var models []securityBlockModel
	if err := db.Order("created_at DESC").Limit(limit).Find(&models).Error; err != nil {
		return nil, err
	}
	out := make([]SecurityBlock, 0, len(models))
	for _, model := range models {
		out = append(out, modelToSecurityBlock(model))
	}
	return out, nil
}

func (r *SecurityRepo) ListActiveBlocks(ctx context.Context, now time.Time) ([]SecurityBlock, error) {
	var models []securityBlockModel
	if err := r.db.WithContext(ctx).Model(&securityBlockModel{}).
		Where("status = ? AND (blocked_until IS NULL OR blocked_until > ?)", SecurityBlockStatusActive, now).
		Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, err
	}
	out := make([]SecurityBlock, 0, len(models))
	for _, model := range models {
		out = append(out, modelToSecurityBlock(model))
	}
	return out, nil
}

func (r *SecurityRepo) ReleaseBlock(ctx context.Context, id string, releasedAt time.Time) (SecurityBlock, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return SecurityBlock{}, err
	}
	res := r.db.WithContext(ctx).Model(&securityBlockModel{}).Where("id = ?", uid).Updates(map[string]any{
		"status":      SecurityBlockStatusReleased,
		"released_at": releasedAt,
		"updated_at":  releasedAt,
	})
	if res.Error != nil {
		return SecurityBlock{}, res.Error
	}
	if res.RowsAffected == 0 {
		return SecurityBlock{}, domain.NewError(domain.CodeNotFound, "封禁记录不存在")
	}
	var model securityBlockModel
	if err := r.db.WithContext(ctx).Where("id = ?", uid).First(&model).Error; err != nil {
		return SecurityBlock{}, notFoundIfNoRows(err, "封禁记录不存在")
	}
	return modelToSecurityBlock(model), nil
}

func (r *SecurityRepo) MarkExpiredBlocks(ctx context.Context, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Model(&securityBlockModel{}).
		Where("status = ? AND blocked_until IS NOT NULL AND blocked_until <= ?", SecurityBlockStatusActive, now).
		Updates(map[string]any{"status": SecurityBlockStatusExpired, "updated_at": now})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

func (r *SecurityRepo) CountBlocksBySubjectSince(ctx context.Context, subjectType, subject string, since time.Time) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&securityBlockModel{}).
		Where("subject_type = ? AND subject = ? AND created_at >= ?", subjectType, subject, since).
		Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

func (r *SecurityRepo) Summary(ctx context.Context, now time.Time) (SecuritySummary, error) {
	since := now.Add(-24 * time.Hour)
	var summary SecuritySummary
	if err := r.db.WithContext(ctx).Model(&securityBlockModel{}).
		Where("status = ? AND (blocked_until IS NULL OR blocked_until > ?)", SecurityBlockStatusActive, now).
		Count(&summary.ActiveBlocks).Error; err != nil {
		return SecuritySummary{}, err
	}
	if err := r.db.WithContext(ctx).Model(&securityEventModel{}).
		Where("event_type = ? AND created_at >= ?", "auth_failed", since).
		Count(&summary.AuthFailures24h).Error; err != nil {
		return SecuritySummary{}, err
	}
	if err := r.db.WithContext(ctx).Model(&securityEventModel{}).
		Where("event_type = ? AND created_at >= ?", "acl_denied", since).
		Count(&summary.ACLDenies24h).Error; err != nil {
		return SecuritySummary{}, err
	}
	const distinctSubjectsSQL = `
		SELECT COUNT(*)
		FROM (
			SELECT DISTINCT subject_type, subject
			FROM security_event
			WHERE created_at >= ? AND subject <> ''
		) AS subjects`
	if err := r.db.WithContext(ctx).Raw(distinctSubjectsSQL, since).Scan(&summary.HighRiskSubjects24h).Error; err != nil {
		return SecuritySummary{}, err
	}
	return summary, nil
}

func clampSecurityLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func modelToSecurityEvent(model securityEventModel) SecurityEvent {
	return SecurityEvent{
		ID:             model.ID,
		EventType:      model.EventType,
		SubjectType:    model.SubjectType,
		Subject:        model.Subject,
		ClientIP:       model.ClientIP,
		APIKeyID:       model.APIKeyID,
		APIKeyPrefix:   model.APIKeyPrefix,
		KeyFingerprint: model.KeyFingerprint,
		Method:         model.Method,
		Path:           model.Path,
		UserAgent:      model.UserAgent,
		Reason:         model.Reason,
		Count:          model.Count,
		CreatedAt:      model.CreatedAt,
	}
}

func modelToSecurityBlock(model securityBlockModel) SecurityBlock {
	return SecurityBlock{
		ID:             model.ID,
		SubjectType:    model.SubjectType,
		Subject:        model.Subject,
		ClientIP:       model.ClientIP,
		APIKeyID:       model.APIKeyID,
		APIKeyPrefix:   model.APIKeyPrefix,
		KeyFingerprint: model.KeyFingerprint,
		Reason:         model.Reason,
		FailureCount:   model.FailureCount,
		Status:         model.Status,
		BlockedUntil:   model.BlockedUntil,
		ReleasedAt:     model.ReleasedAt,
		CreatedAt:      model.CreatedAt,
		UpdatedAt:      model.UpdatedAt,
	}
}
