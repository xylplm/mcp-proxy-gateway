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

func TestRiskJobActiveStatusesForProvider(t *testing.T) {
	repos, mock := newMockRepositories(t)
	mock.ExpectQuery(`SELECT "status" FROM "risk_assessment_job" WHERE provider_id = \$1 AND status IN \(\$2,\$3\) GROUP BY "status"`).
		WithArgs("provider-1", risk.JobQueued, risk.JobRunning).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(risk.JobRunning).AddRow(risk.JobQueued))

	statuses, err := repos.RiskJob.ActiveStatusesForProvider(context.Background(), "provider-1")
	if err != nil {
		t.Fatalf("ActiveStatusesForProvider returned error: %v", err)
	}
	if len(statuses) != 2 || statuses[0] != risk.JobRunning || statuses[1] != risk.JobQueued {
		t.Fatalf("active statuses = %v, want [running queued]", statuses)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}
