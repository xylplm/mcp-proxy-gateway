package store

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newMockRepositories(t *testing.T) (*Repositories, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open gorm mock db: %v", err)
	}
	return NewRepositories(db), mock
}

func TestRepositoriesWithTransactionCommitsOnSuccess(t *testing.T) {
	repos, mock := newMockRepositories(t)
	mock.ExpectBegin()
	mock.ExpectCommit()

	called := false
	err := repos.WithTransaction(context.Background(), func(txRepos *Repositories) error {
		called = true
		if txRepos == repos {
			t.Fatalf("transaction callback should receive repositories bound to tx")
		}
		if txRepos.Upstream == nil || txRepos.APIKey == nil {
			t.Fatalf("transaction repositories should be fully initialized")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithTransaction returned error: %v", err)
	}
	if !called {
		t.Fatalf("transaction callback was not called")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestRepositoriesWithTransactionRollsBackOnError(t *testing.T) {
	repos, mock := newMockRepositories(t)
	mock.ExpectBegin()
	mock.ExpectRollback()
	wantErr := errors.New("boom")

	err := repos.WithTransaction(context.Background(), func(txRepos *Repositories) error {
		if txRepos == nil {
			t.Fatalf("transaction repositories should not be nil")
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("want callback error %v, got %v", wantErr, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}
