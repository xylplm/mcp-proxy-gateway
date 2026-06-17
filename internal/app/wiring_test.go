package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/aggregation"
	"github.com/myGithub/mcp-proxy-gateway/internal/apikey"
	"github.com/myGithub/mcp-proxy-gateway/internal/auth"
	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/manager"
	"github.com/myGithub/mcp-proxy-gateway/internal/transport"
)

// inMemoryConfigStore 是 auth.ConfigStore 的内存替身，供 EnsureJWTSecret 单测使用。
type inMemoryConfigStore struct {
	cfg config.YAMLConfig
}

func newInMemoryConfigStore() *inMemoryConfigStore {
	return &inMemoryConfigStore{cfg: config.DefaultYAMLConfig()}
}

func (s *inMemoryConfigStore) Config() config.YAMLConfig { return s.cfg }

func (s *inMemoryConfigStore) Save(cfg config.YAMLConfig) error {
	s.cfg = cfg
	return nil
}

// TestRetryPolicyFromConfig 验证连接配置到退避策略的映射（秒 → time.Duration，字段对应）。
func TestRetryPolicyFromConfig(t *testing.T) {
	c := config.ConnectionConfig{
		ConnectTimeoutS:      30,
		RetryInitialBackoffS: 2,
		RetryMaxBackoffS:     60,
		RetryMultiplier:      3,
		FailureThreshold:     7,
	}
	got := retryPolicyFromConfig(c)
	want := manager.RetryPolicy{
		ConnectTimeout:   30 * time.Second,
		InitialBackoff:   2 * time.Second,
		MaxBackoff:       60 * time.Second,
		Multiplier:       3,
		FailureThreshold: 7,
	}
	if got != want {
		t.Fatalf("retryPolicyFromConfig = %+v, want %+v", got, want)
	}
}

// TestXiaoZhiBackoffFromConfig 验证小智重连退避策略映射。
func TestXiaoZhiBackoffFromConfig(t *testing.T) {
	c := config.ConnectionConfig{
		RetryInitialBackoffS: 1,
		RetryMaxBackoffS:     120,
		RetryMultiplier:      2,
	}
	got := xiaozhiBackoffFromConfig(c)
	if got.Initial != time.Second || got.Max != 120*time.Second || got.Multiplier != 2 {
		t.Fatalf("xiaozhiBackoffFromConfig = %+v", got)
	}
}

// TestEnsureJWTSecretGeneratesAndPersists 验证 EnsureJWTSecret 在密钥为空时生成非空密钥并写回，
// 在密钥已存在时保持不变（幂等）。
func TestEnsureJWTSecretGeneratesAndPersists(t *testing.T) {
	t.Run("为空时生成并写回", func(t *testing.T) {
		store := newInMemoryConfigStore()
		if err := auth.EnsureJWTSecret(store, slog.Default()); err != nil {
			t.Fatalf("生成 JWT 密钥不应失败：%v", err)
		}
		secret := store.cfg.JWTSecret
		if secret == "" {
			t.Fatal("生成后 jwt_secret 不应为空")
		}
		if _, err := base64.RawURLEncoding.DecodeString(secret); err != nil {
			t.Fatalf("生成的 jwt_secret 应为合法 base64url：%v", err)
		}
	})

	t.Run("已存在时保持不变（幂等）", func(t *testing.T) {
		store := newInMemoryConfigStore()
		store.cfg.JWTSecret = "preset-secret"
		if err := auth.EnsureJWTSecret(store, slog.Default()); err != nil {
			t.Fatalf("已有密钥时不应失败：%v", err)
		}
		if store.cfg.JWTSecret != "preset-secret" {
			t.Fatalf("已有密钥应保持不变，实际 %q", store.cfg.JWTSecret)
		}
	})
}

// TestResolveAPIKeyID 验证从已鉴权上下文取出 API Key 标识；无上下文时返回 ok=false。
func TestResolveAPIKeyID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 无 API Key 上下文：ok=false。
	c, _ := gin.CreateTestContext(nil)
	if _, ok := resolveAPIKeyID(c); ok {
		t.Fatal("无 API Key 上下文时应返回 ok=false")
	}

	// 写入元数据后：返回对应 ID。
	c2, _ := gin.CreateTestContext(nil)
	c2.Set("apikey.metadata", apikey.Metadata{ID: "key-123"})
	id, ok := resolveAPIKeyID(c2)
	if !ok || id != "key-123" {
		t.Fatalf("resolveAPIKeyID = (%q,%v), want (key-123,true)", id, ok)
	}
}

// fakeToolSession 是 transport.UpstreamSession 的内存实现，用于验证会话注册与调用转发。
type fakeToolSession struct {
	tools      []domain.ToolDef
	closed     bool
	callResult domain.ToolResult
}

func (f *fakeToolSession) Connect(ctx context.Context) error { return nil }
func (f *fakeToolSession) ListTools(ctx context.Context) ([]domain.ToolDef, error) {
	return f.tools, nil
}
func (f *fakeToolSession) CallTool(ctx context.Context, name string, args json.RawMessage) (domain.ToolResult, error) {
	return f.callResult, nil
}
func (f *fakeToolSession) Close() error { f.closed = true; return nil }

// fakeFactory 构造预置的 fakeToolSession。
type fakeFactory struct {
	sess *fakeToolSession
	err  error
}

func (f fakeFactory) NewSession(cfg domain.UpstreamConfig) (transport.UpstreamSession, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.sess, nil
}
func (f fakeFactory) Supports(t domain.TransportType) bool { return true }

// TestSessionDialerRegistersAndUnregisters 验证：Dial 成功后会话被登记，可经 Session 取出；
// Conn.Close 后会话被注销，Session 返回 ok=false（Req 10.3/10.5 的会话可用性语义）。
func TestSessionDialerRegistersAndUnregisters(t *testing.T) {
	sess := &fakeToolSession{}
	d := newSessionDialer(fakeFactory{sess: sess})

	// 拨号前：无会话。
	if _, ok := d.Session("u1"); ok {
		t.Fatal("拨号前不应存在会话")
	}

	conn, err := d.Dial(context.Background(), "u1", domain.UpstreamConfig{})
	if err != nil {
		t.Fatalf("Dial 失败：%v", err)
	}

	// 拨号后：会话已登记且可作为 ToolCaller 取出。
	caller, ok := d.Session("u1")
	if !ok || caller == nil {
		t.Fatal("拨号后应能取出已登记会话")
	}

	// 关闭连接：会话注销且底层 session 被关闭。
	if err := conn.Close(); err != nil {
		t.Fatalf("Close 失败：%v", err)
	}
	if !sess.closed {
		t.Fatal("Close 应关闭底层会话")
	}
	if _, ok := d.Session("u1"); ok {
		t.Fatal("关闭后会话应被注销")
	}
}

// 编译期断言：fakeToolSession 满足 aggregation.ToolCaller（CallTool 形态正确）。
var _ aggregation.ToolCaller = (*fakeToolSession)(nil)

// TestSessionDialerDialError 验证：会话构造/连接失败时 Dial 返回错误且不登记会话。
func TestSessionDialerDialError(t *testing.T) {
	d := newSessionDialer(fakeFactory{err: errors.New("boom")})
	if _, err := d.Dial(context.Background(), "u1", domain.UpstreamConfig{}); err == nil {
		t.Fatal("工厂出错时 Dial 应返回错误")
	}
	if _, ok := d.Session("u1"); ok {
		t.Fatal("Dial 失败不应登记会话")
	}
}
