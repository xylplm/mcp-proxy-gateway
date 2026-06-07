package store

import (
	"context"
	"errors"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// asAPIError 将 error 断言为 *domain.APIError，便于校验错误类别码。
func asAPIError(t *testing.T, err error) *domain.APIError {
	t.Helper()
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望 *domain.APIError，实际为 %T: %v", err, err)
	}
	return apiErr
}

// TestNewPGPoolEmptyDSN 验证空 DSN 立即返回校验错误，不发起网络连接。
func TestNewPGPoolEmptyDSN(t *testing.T) {
	pool, err := NewPGPool(context.Background(), "")
	if pool != nil {
		t.Fatal("空 DSN 不应返回连接池")
	}
	if err == nil {
		t.Fatal("空 DSN 应返回错误")
	}
	if got := asAPIError(t, err); got.Code != domain.CodeValidation {
		t.Errorf("期望错误码 %s，实际 %s", domain.CodeValidation, got.Code)
	}
}

// TestNewPGPoolInvalidDSN 验证非法 DSN 在解析阶段即返回校验错误。
func TestNewPGPoolInvalidDSN(t *testing.T) {
	// 含非法端口的 URL 形式 DSN，应在 ParseConfig 阶段失败。
	pool, err := NewPGPool(context.Background(), "postgres://user:pass@host:notaport/db")
	if pool != nil {
		t.Fatal("非法 DSN 不应返回连接池")
	}
	if err == nil {
		t.Fatal("非法 DSN 应返回错误")
	}
	if got := asAPIError(t, err); got.Code != domain.CodeValidation {
		t.Errorf("期望错误码 %s，实际 %s", domain.CodeValidation, got.Code)
	}
}

// TestNewRedisClientEmptyAddr 验证空地址返回校验错误。
func TestNewRedisClientEmptyAddr(t *testing.T) {
	client, err := NewRedisClient("", "")
	if client != nil {
		t.Fatal("空地址不应返回客户端")
	}
	if err == nil {
		t.Fatal("空地址应返回错误")
	}
	if got := asAPIError(t, err); got.Code != domain.CodeValidation {
		t.Errorf("期望错误码 %s，实际 %s", domain.CodeValidation, got.Code)
	}
}

// TestNewRedisClientConstructs 验证给定合法地址时返回非空客户端（惰性连接，不发起网络请求）。
func TestNewRedisClientConstructs(t *testing.T) {
	client, err := NewRedisClient("localhost:6379", "secret")
	if err != nil {
		t.Fatalf("构造 Redis 客户端不应失败: %v", err)
	}
	if client == nil {
		t.Fatal("应返回非空 Redis 客户端")
	}
	t.Cleanup(func() { _ = client.Close() })
}

// TestPingRedisNilClient 验证对 nil 客户端 Ping 返回校验错误。
func TestPingRedisNilClient(t *testing.T) {
	if err := PingRedis(context.Background(), nil); err == nil {
		t.Fatal("nil 客户端应返回错误")
	}
}

// TestRunMigrationsEmptyDSN 验证空 DSN 时迁移立即返回校验错误。
func TestRunMigrationsEmptyDSN(t *testing.T) {
	err := RunMigrations("", nil)
	if err == nil {
		t.Fatal("空 DSN 应返回错误")
	}
	if got := asAPIError(t, err); got.Code != domain.CodeValidation {
		t.Errorf("期望错误码 %s，实际 %s", domain.CodeValidation, got.Code)
	}
}
