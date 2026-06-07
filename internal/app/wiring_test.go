package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/aggregation"
	"github.com/myGithub/mcp-proxy-gateway/internal/apikey"
	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/manager"
	"github.com/myGithub/mcp-proxy-gateway/internal/transport"
)

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

// TestSigningKeyDeterministicAndNonEmpty 验证 JWT 签名密钥派生稳定且非空、且不等于原始密钥。
func TestSigningKeyDeterministicAndNonEmpty(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef"
	k1 := signingKey(key)
	k2 := signingKey(key)
	if len(k1) == 0 {
		t.Fatal("签名密钥不应为空")
	}
	if string(k1) != string(k2) {
		t.Fatal("相同主密钥应派生出相同签名密钥")
	}
	if string(k1) == key {
		t.Fatal("签名密钥不应直接等于加密主密钥")
	}
	if string(signingKey("another-key")) == string(k1) {
		t.Fatal("不同主密钥应派生出不同签名密钥")
	}
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
