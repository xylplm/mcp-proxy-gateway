package apikey

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

func init() {
	// 关闭 gin 调试日志，保持测试输出干净。
	gin.SetMode(gin.TestMode)
}

// TestMatchCIDREmptyWhitelistAllows 验证未配置白名单（空/nil）时不限制来源，一律放行（Req 13.9）。
func TestMatchCIDREmptyWhitelistAllows(t *testing.T) {
	cases := []struct {
		name  string
		cidrs []string
	}{
		{"nil 白名单", nil},
		{"空白名单", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, err := MatchCIDR("203.0.113.7", c.cidrs)
			if err != nil {
				t.Fatalf("空白名单不应返回错误：%v", err)
			}
			if !ok {
				t.Error("未配置白名单时应放行任意来源")
			}
		})
	}
}

// TestMatchCIDRHitAndMiss 验证白名单命中放行、未命中拒绝（Req 13.9、13.10）。
func TestMatchCIDRHitAndMiss(t *testing.T) {
	cases := []struct {
		name     string
		remote   string
		cidrs    []string
		expectOK bool
	}{
		{"网段内放行", "10.1.2.3", []string{"10.0.0.0/8"}, true},
		{"网段外拒绝", "11.1.2.3", []string{"10.0.0.0/8"}, false},
		{"单 IP 精确命中", "1.2.3.4", []string{"1.2.3.4"}, true},
		{"单 IP 不命中相邻地址", "1.2.3.5", []string{"1.2.3.4"}, false},
		{"多条目命中其一", "192.168.5.9", []string{"10.0.0.0/8", "192.168.0.0/16"}, true},
		{"多条目全未命中", "172.16.0.1", []string{"10.0.0.0/8", "192.168.0.0/16"}, false},
		{"边界：网段首地址", "10.0.0.0", []string{"10.0.0.0/8"}, true},
		{"边界：网段末地址", "10.255.255.255", []string{"10.0.0.0/8"}, true},
		{"IPv6 网段命中", "2001:db8::1", []string{"2001:db8::/32"}, true},
		{"IPv6 网段不命中", "2001:dead::1", []string{"2001:db8::/32"}, false},
		{"IPv4-mapped IPv6 命中等价 IPv4 网段", "::ffff:10.0.0.1", []string{"10.0.0.0/8"}, true},
		{"带掩码单 IP /32", "1.2.3.4", []string{"1.2.3.4/32"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, err := MatchCIDR(c.remote, c.cidrs)
			if err != nil {
				t.Fatalf("合法输入不应返回错误：%v", err)
			}
			if ok != c.expectOK {
				t.Errorf("来源 %q 对白名单 %v 期望放行=%v，实际=%v", c.remote, c.cidrs, c.expectOK, ok)
			}
		})
	}
}

// TestMatchCIDRInvalidRemoteIP 验证来源地址非法时返回 VALIDATION 且不放行。
func TestMatchCIDRInvalidRemoteIP(t *testing.T) {
	ok, err := MatchCIDR("not-an-ip", []string{"10.0.0.0/8"})
	if ok {
		t.Error("来源地址非法时不应放行")
	}
	apiErr := asACLAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
	}
}

// TestMatchCIDRInvalidEntry 验证白名单条目非法时返回 VALIDATION 且不放行。
func TestMatchCIDRInvalidEntry(t *testing.T) {
	ok, err := MatchCIDR("10.0.0.1", []string{"garbage/99"})
	if ok {
		t.Error("白名单条目非法时不应放行")
	}
	apiErr := asACLAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
	}
}

// testACLLister 是 ACLLister 窄接口的内存实现，便于脱离真实数据库测试 ACLGuard。
type testACLLister struct {
	// byKey 以 API Key 标识为键存放其白名单记录。
	byKey map[string][]store.ACLEntry
	// listErr 用于注入加载失败，验证 fail-closed 行为。
	listErr error
}

func (l *testACLLister) ListByAPIKey(_ context.Context, apiKeyID string) ([]store.ACLEntry, error) {
	if l.listErr != nil {
		return nil, l.listErr
	}
	return l.byKey[apiKeyID], nil
}

// asACLAPIError 将 error 断言为 *domain.APIError。
func asACLAPIError(t *testing.T, err error) *domain.APIError {
	t.Helper()
	if err == nil {
		t.Fatal("期望返回错误，但得到 nil")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望错误类型为 *domain.APIError，实际为 %T：%v", err, err)
	}
	return apiErr
}

// TestACLGuardAllowNoWhitelist 验证未配置白名单时不限制来源（Req 13.9）。
func TestACLGuardAllowNoWhitelist(t *testing.T) {
	guard := NewACLGuard(&testACLLister{byKey: map[string][]store.ACLEntry{}}, nil)
	ok, err := guard.Allow(context.Background(), "key-1", "203.0.113.9")
	if err != nil {
		t.Fatalf("未配置白名单不应返回错误：%v", err)
	}
	if !ok {
		t.Error("未配置白名单时应放行任意来源")
	}
}

// TestACLGuardAllowHitAndMiss 验证配置白名单后命中放行、未命中拒绝（Req 13.9、13.10）。
func TestACLGuardAllowHitAndMiss(t *testing.T) {
	lister := &testACLLister{byKey: map[string][]store.ACLEntry{
		"key-1": {{CIDR: "10.0.0.0/8"}, {CIDR: "192.168.1.10/32"}},
	}}
	guard := NewACLGuard(lister, nil)

	ok, err := guard.Allow(context.Background(), "key-1", "10.9.9.9")
	if err != nil || !ok {
		t.Errorf("命中网段应放行，ok=%v err=%v", ok, err)
	}

	ok, err = guard.Allow(context.Background(), "key-1", "8.8.8.8")
	if err != nil {
		t.Fatalf("未命中不应返回错误：%v", err)
	}
	if ok {
		t.Error("来源不在白名单内应被拒绝")
	}
}

// TestACLGuardAllowPropagatesLoadError 验证白名单加载失败时透传错误（供中间件 fail-closed）。
func TestACLGuardAllowPropagatesLoadError(t *testing.T) {
	lister := &testACLLister{listErr: domain.NewError(domain.CodeValidation, "数据库不可用")}
	guard := NewACLGuard(lister, nil)
	if _, err := guard.Allow(context.Background(), "key-1", "10.0.0.1"); err == nil {
		t.Error("白名单加载失败时应透传错误")
	}
}

// newACLTestRouter 构造挂载了 ACL 中间件的路由。resolve 模拟前序鉴权写入的 API Key。
func newACLTestRouter(guard *ACLGuard, resolve ACLKeyResolver) *gin.Engine {
	r := gin.New()
	r.GET("/mcp/http", guard.Middleware(resolve), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

// doACLRequest 以指定来源地址发起请求并返回响应记录器。
func doACLRequest(r *gin.Engine, remoteAddr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/mcp/http", nil)
	req.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// resolverFor 返回固定提供给定 API Key 标识的解析器。
func resolverFor(id string) ACLKeyResolver {
	return func(*gin.Context) (Metadata, bool) {
		return Metadata{ID: id}, true
	}
}

// TestMiddlewareForbidsOutsideWhitelist 验证来源不在白名单时返回 HTTP 403 FORBIDDEN（Req 13.10）。
func TestMiddlewareForbidsOutsideWhitelist(t *testing.T) {
	lister := &testACLLister{byKey: map[string][]store.ACLEntry{
		"key-1": {{CIDR: "10.0.0.0/8"}},
	}}
	guard := NewACLGuard(lister, nil)
	r := newACLTestRouter(guard, resolverFor("key-1"))

	w := doACLRequest(r, "8.8.8.8:12345")
	if w.Code != http.StatusForbidden {
		t.Fatalf("期望 HTTP 403，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); !contains(got, string(domain.CodeForbidden)) {
		t.Errorf("响应体应包含错误码 %q，实际 %s", domain.CodeForbidden, got)
	}
}

// TestMiddlewareAllowsWithinWhitelist 验证来源命中白名单时放行（Req 13.9）。
func TestMiddlewareAllowsWithinWhitelist(t *testing.T) {
	lister := &testACLLister{byKey: map[string][]store.ACLEntry{
		"key-1": {{CIDR: "10.0.0.0/8"}},
	}}
	guard := NewACLGuard(lister, nil)
	r := newACLTestRouter(guard, resolverFor("key-1"))

	w := doACLRequest(r, "10.1.2.3:9999")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
}

// TestMiddlewareAllowsWhenNoWhitelist 验证未配置白名单时放行任意来源（Req 13.9）。
func TestMiddlewareAllowsWhenNoWhitelist(t *testing.T) {
	guard := NewACLGuard(&testACLLister{byKey: map[string][]store.ACLEntry{}}, nil)
	r := newACLTestRouter(guard, resolverFor("key-1"))

	w := doACLRequest(r, "203.0.113.7:5555")
	if w.Code != http.StatusOK {
		t.Fatalf("未配置白名单应放行，期望 HTTP 200，实际 %d", w.Code)
	}
}

// TestMiddlewareFailsClosedOnLoadError 验证白名单加载失败时 fail-closed 返回 403（安全优先）。
func TestMiddlewareFailsClosedOnLoadError(t *testing.T) {
	lister := &testACLLister{listErr: domain.NewError(domain.CodeValidation, "数据库不可用")}
	guard := NewACLGuard(lister, nil)
	r := newACLTestRouter(guard, resolverFor("key-1"))

	w := doACLRequest(r, "10.1.2.3:9999")
	if w.Code != http.StatusForbidden {
		t.Fatalf("加载失败应 fail-closed 返回 HTTP 403，实际 %d", w.Code)
	}
}

// TestMiddlewarePassThroughWithoutKey 验证上下文无 API Key 时放行（交由前序鉴权拒绝）。
func TestMiddlewarePassThroughWithoutKey(t *testing.T) {
	guard := NewACLGuard(&testACLLister{byKey: map[string][]store.ACLEntry{}}, nil)
	resolve := func(*gin.Context) (Metadata, bool) { return Metadata{}, false }
	r := newACLTestRouter(guard, resolve)

	w := doACLRequest(r, "8.8.8.8:1234")
	if w.Code != http.StatusOK {
		t.Fatalf("无 API Key 时应放行，期望 HTTP 200，实际 %d", w.Code)
	}
}

// TestMiddlewarePassThroughNilResolver 验证 resolve 为 nil 时放行，避免全量拒绝。
func TestMiddlewarePassThroughNilResolver(t *testing.T) {
	guard := NewACLGuard(&testACLLister{byKey: map[string][]store.ACLEntry{}}, nil)
	r := newACLTestRouter(guard, nil)

	w := doACLRequest(r, "8.8.8.8:1234")
	if w.Code != http.StatusOK {
		t.Fatalf("resolve 为 nil 时应放行，期望 HTTP 200，实际 %d", w.Code)
	}
}

// contains 是不依赖额外导入的子串判定辅助。
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
