package store

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/risk"
)

type RiskJobRepo struct{ db *gorm.DB }

func NewRiskJobRepo(db *gorm.DB) *RiskJobRepo { return &RiskJobRepo{db: db} }

func (r *RiskJobRepo) Create(ctx context.Context, job risk.AssessmentJob) (risk.AssessmentJob, error) {
	payload, err := json.Marshal(job.ScopePayload)
	if err != nil {
		return risk.AssessmentJob{}, err
	}
	now := time.Now().UTC()
	model := riskAssessmentJobModel{ID: newUUID(), ProviderID: job.ProviderID, Scope: job.Scope,
		ScopePayload: JSONB(payload), Status: string(risk.JobQueued), RequestedCount: job.RequestedCount,
		ErrorCounts: JSONB(`{}`),
		CreatedAt:   now, UpdatedAt: now}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return risk.AssessmentJob{}, err
	}
	return modelToRiskJob(model), nil
}

func (r *RiskJobRepo) Get(ctx context.Context, id string) (risk.AssessmentJob, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return risk.AssessmentJob{}, err
	}
	var model riskAssessmentJobModel
	if err := r.db.WithContext(ctx).Where("id = ?", uid).First(&model).Error; err != nil {
		return risk.AssessmentJob{}, notFoundIfNoRows(err, "评级任务不存在")
	}
	return modelToRiskJob(model), nil
}

func (r *RiskJobRepo) List(ctx context.Context, limit int) ([]risk.AssessmentJob, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var models []riskAssessmentJobModel
	if err := r.db.WithContext(ctx).Order("created_at DESC").Limit(limit).Find(&models).Error; err != nil {
		return nil, err
	}
	out := make([]risk.AssessmentJob, 0, len(models))
	for _, model := range models {
		out = append(out, modelToRiskJob(model))
	}
	return out, nil
}

func (r *RiskJobRepo) Cancel(ctx context.Context, id string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).Model(&riskAssessmentJobModel{}).
		Where("id = ? AND status IN ?", uid, []risk.JobStatus{risk.JobQueued, risk.JobRunning}).
		Updates(map[string]any{"status": risk.JobCancelled, "finished_at": now, "updated_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.NewError(domain.CodeConflict, "评级任务不存在或已结束")
	}
	return nil
}

func (r *RiskJobRepo) RecoverRunning(ctx context.Context) error {
	return r.db.WithContext(ctx).Model(&riskAssessmentJobModel{}).Where("status = ?", risk.JobRunning).
		Updates(map[string]any{"status": risk.JobQueued, "started_at": nil, "updated_at": time.Now().UTC()}).Error
}

func (r *RiskJobRepo) ListQueued(ctx context.Context) ([]risk.AssessmentJob, error) {
	var models []riskAssessmentJobModel
	if err := r.db.WithContext(ctx).Where("status = ?", risk.JobQueued).Order("created_at ASC").Find(&models).Error; err != nil {
		return nil, err
	}
	out := make([]risk.AssessmentJob, 0, len(models))
	for _, model := range models {
		out = append(out, modelToRiskJob(model))
	}
	return out, nil
}

func (r *RiskJobRepo) SetRunning(ctx context.Context, id string) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).Model(&riskAssessmentJobModel{}).Where("id = ? AND status = ?", id, risk.JobQueued).
		Updates(map[string]any{"status": risk.JobRunning, "started_at": now, "updated_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.NewError(domain.CodeConflict, "评级任务不处于等待状态")
	}
	return nil
}

func (r *RiskJobRepo) UpdateProgress(ctx context.Context, job risk.AssessmentJob) error {
	errorCounts, _ := json.Marshal(job.ErrorCounts)
	updates := map[string]any{"status": job.Status, "processed_count": job.ProcessedCount, "success_count": job.SuccessCount,
		"review_count": job.ReviewCount, "failure_count": job.FailureCount, "retry_count": job.RetryCount,
		"split_count": job.SplitCount, "error_counts": JSONB(errorCounts), "last_error": job.LastError, "updated_at": time.Now().UTC()}
	if job.Status == risk.JobCompleted || job.Status == risk.JobPartial || job.Status == risk.JobFailed || job.Status == risk.JobCancelled {
		now := time.Now().UTC()
		updates["finished_at"] = now
	}
	return r.db.WithContext(ctx).Model(&riskAssessmentJobModel{}).
		Where("id = ? AND status <> ?", job.ID, risk.JobCancelled).
		Updates(updates).Error
}

func modelToRiskJob(m riskAssessmentJobModel) risk.AssessmentJob {
	payload := map[string]any{}
	errorCounts := map[string]int{}
	_ = json.Unmarshal(m.ScopePayload, &payload)
	_ = json.Unmarshal(m.ErrorCounts, &errorCounts)
	return risk.AssessmentJob{ID: m.ID, ProviderID: m.ProviderID, Scope: m.Scope, ScopePayload: payload,
		Status: risk.JobStatus(m.Status), RequestedCount: m.RequestedCount, ProcessedCount: m.ProcessedCount,
		SuccessCount: m.SuccessCount, ReviewCount: m.ReviewCount, FailureCount: m.FailureCount,
		RetryCount: m.RetryCount, SplitCount: m.SplitCount, ErrorCounts: errorCounts,
		LastError: m.LastError, CreatedAt: m.CreatedAt, StartedAt: m.StartedAt, FinishedAt: m.FinishedAt, UpdatedAt: m.UpdatedAt}
}
