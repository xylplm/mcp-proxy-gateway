package store

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/myGithub/mcp-proxy-gateway/internal/risk"
)

func TestRiskJobUpdateProgressDoesNotOverwriteCancelledJob(t *testing.T) {
	repos, mock := newMockRepositories(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE .*risk_assessment_job.*status <>").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repos.RiskJob.UpdateProgress(context.Background(), risk.AssessmentJob{
		ID:          "0198d145-cadb-7f80-8000-000000000001",
		Status:      risk.JobRunning,
		ErrorCounts: map[string]int{},
	})
	if err != nil {
		t.Fatalf("UpdateProgress returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}
