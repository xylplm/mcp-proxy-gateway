package apikey

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// fakeAuthLookup 是 AuthKeyLookup 窄接口的内存实现，便于脱离真实数据库测试鉴权逻辑。
//
// 它以 SHA-256 哈希（字符串形式）为键存放 API Key 行，模拟 GetByHash 的按哈希等值查询；
// 无匹配时返回 NOT_FOUND，与 *store.APIKeyRepo.GetByHash 的契约一致。lookupErr 可注入
// 以模拟查库基础设施异常，验证 fail-closed 行为。
type fakeAuthLookup struct {
	byHash    map[string]store.APIKey
	lookupErr error
}

func newFakeAuthLookup() *fakeAuthLookup {
	return &fakeAuthLookup{byHash: make(map[string]store.APIKey)}
}

// add 以明文密钥注册一条 API Key 行，内部按 SHA-256 建立索引。
func (l *fakeAuthLookup) add(plaintext string, row store.APIKey) {
	sum := sha256.Sum256([]byte(plaintext))
	l.byHash[string(sum[:])] = row
}

func (l *fakeAuthLookup) GetByHash(_ context.Context, keyHash []byte) (store.APIKey, error) {
	if l.lookupErr != nil {
		return store.APIKey{}, l.lookupErr
	}
	row, ok := l.byHash[string(keyHash)]
	if !ok {
		return store.APIKey{}, domain.NewError(domain.CodeNotFound, "API Key 不存在")
	}
	return row, nil
}

// timePtr 返回指向所给时刻的指针，便于构造可选的有效期字段。
func timePtr(t time.Time) *time.Time { return &t }

// newAuthTestRouter 构造挂载了鉴权中间件的路由；其受保护处理器在放行后回显上下文中的 API Key ID，
// 以便断言鉴权通过时元数据被正确注入。
func newAuthTestRouter(a *Authenticator) *gin.Engine {
	r := gin.New()
	r.GET("/mcp/http", a.Middleware(), func(c *gin.Context) {
		meta, ok := MetadataFromContext(c)
		if !ok {
			// 放行后上下文中必须存在元数据；缺失说明注入逻辑有误。
			c.JSON(http.StatusInternalServerError, gin.H{"error": "上下文缺少 API Key 元数据"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": meta.ID})
	})
	return r
}

// TestAuthenticateValidKey 验证合法（存在/启用/未过期）的 API Key 通过鉴权并返回元数据（Req 11.9、12.5）。
func TestAuthenticateValidKey(t *testing.T) {
	lookup := newFakeAuthLookup()
	lookup.add("mpg_valid_plaintext", store.APIKey{ID: "key-1", Name: "ok", Enabled: true})
	a := NewAuthenticator(lookup, nil)

	meta, ok, err := a.Authenticate(context.Background(), "mpg_valid_plaintext", time.Unix(1000, 0))
	if err != nil {
		t.Fatalf("合法 API Key 不应返回错误：%v", err)
	}
	if !ok {
		t.Fatal("合法 API Key 应通过鉴权")
	}
	if meta.ID != "key-1" {
		t.Errorf("期望返回 ID=key-1，实际 %q", meta.ID)
	}
}

// TestAuthenticateMissingKey 验证空 API Key 被拒绝并返回 UNAUTHORIZED。
func TestAuthenticateMissingKey(t *testing.T) {
	a := NewAuthenticator(newFakeAuthLookup(), nil)
	_, ok, err := a.Authenticate(context.Background(), "   ", time.Unix(1000, 0))
	if ok {
		t.Fatal("空 API Key 不应通过鉴权")
	}
	apiErr := asACLAPIError(t, err)
	if apiErr.Code != domain.CodeUnauthorized {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeUnauthorized, apiErr.Code)
	}
}

// TestAuthenticateNonexistentKey 验证不存在的 API Key 被拒绝并返回 UNAUTHORIZED。
func TestAuthenticateNonexistentKey(t *testing.T) {
	a := NewAuthenticator(newFakeAuthLookup(), nil)
	_, ok, err := a.Authenticate(context.Background(), "mpg_unknown", time.Unix(1000, 0))
	if ok {
		t.Fatal("不存在的 API Key 不应通过鉴权")
	}
	apiErr := asACLAPIError(t, err)
	if apiErr.Code != domain.CodeUnauthorized {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeUnauthorized, apiErr.Code)
	}
}

// TestAuthenticateDisabledKey 验证已停用的 API Key 被拒绝并返回 UNAUTHORIZED（Req 12.4、12.5）。
func TestAuthenticateDisabledKey(t *testing.T) {
	lookup := newFakeAuthLookup()
	lookup.add("mpg_disabled", store.APIKey{ID: "key-2", Enabled: false})
	a := NewAuthenticator(lookup, nil)

	_, ok, err := a.Authenticate(context.Background(), "mpg_disabled", time.Unix(1000, 0))
	if ok {
		t.Fatal("已停用的 API Key 不应通过鉴权")
	}
	apiErr := asACLAPIError(t, err)
	if apiErr.Code != domain.CodeUnauthorized {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeUnauthorized, apiErr.Code)
	}
}

// TestAuthenticateExpiredKey 验证已超过有效期的 API Key 被拒绝并返回 UNAUTHORIZED（Req 12.5、12.6）。
func TestAuthenticateExpiredKey(t *testing.T) {
	lookup := newFakeAuthLookup()
	expiry := time.Unix(900, 0)
	lookup.add("mpg_expired", store.APIKey{ID: "key-3", Enabled: true, ExpiresAt: timePtr(expiry)})
	a := NewAuthenticator(lookup, nil)

	// now 晚于有效期，应判定为过期。
	_, ok, err := a.Authenticate(context.Background(), "mpg_expired", time.Unix(1000, 0))
	if ok {
		t.Fatal("已过期的 API Key 不应通过鉴权")
	}
	apiErr := asACLAPIError(t, err)
	if apiErr.Code != domain.CodeUnauthorized {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeUnauthorized, apiErr.Code)
	}
}

// TestAuthenticateFailClosedOnLookupError 验证查库异常时 fail-closed 返回 UNAUTHORIZED（安全优先）。
func TestAuthenticateFailClosedOnLookupError(t *testing.T) {
	lookup := newFakeAuthLookup()
	lookup.lookupErr = domain.NewError(domain.CodeValidation, "数据库不可用")
	a := NewAuthenticator(lookup, nil)

	_, ok, err := a.Authenticate(context.Background(), "mpg_any", time.Unix(1000, 0))
	if ok {
		t.Fatal("查库异常时应 fail-closed 拒绝")
	}
	apiErr := asACLAPIError(t, err)
	if apiErr.Code != domain.CodeUnauthorized {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeUnauthorized, apiErr.Code)
	}
}

// doAuthRequest 以指定的 API Key 提取方式发起请求并返回响应记录器。
//   - header 非空时设置 X-API-Key 头；
//   - bearer 非空时设置 Authorization: Bearer 头；
//   - query 非空时附加 api_key 查询参数。
func doAuthRequest(r *gin.Engine, header, bearer, query string) *httptest.ResponseRecorder {
	target := "/mcp/http"
	if query != "" {
		target += "?" + apiKeyQueryParam + "=" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if header != "" {
		req.Header.Set(apiKeyHeader, header)
	}
	if bearer != "" {
		req.Header.Set(authorizationHeaderName, "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestMiddlewareRejectsMissingKey 验证无 API Key 时返回 HTTP 401 且不路由到处理器（Req 11.9、12.5）。
func TestMiddlewareRejectsMissingKey(t *testing.T) {
	a := NewAuthenticator(newFakeAuthLookup(), nil)
	r := newAuthTestRouter(a)

	w := doAuthRequest(r, "", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("无 API Key 期望 HTTP 401，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); !contains(got, string(domain.CodeUnauthorized)) {
		t.Errorf("响应体应包含错误码 %q，实际 %s", domain.CodeUnauthorized, got)
	}
}

// TestMiddlewareRejectsNonexistentKey 验证不存在的 API Key 返回 HTTP 401（Req 12.5）。
func TestMiddlewareRejectsNonexistentKey(t *testing.T) {
	a := NewAuthenticator(newFakeAuthLookup(), nil)
	r := newAuthTestRouter(a)

	w := doAuthRequest(r, "mpg_unknown", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("不存在的 API Key 期望 HTTP 401，实际 %d", w.Code)
	}
}

// TestMiddlewareRejectsDisabledKey 验证已停用的 API Key 返回 HTTP 401（Req 12.4、12.5）。
func TestMiddlewareRejectsDisabledKey(t *testing.T) {
	lookup := newFakeAuthLookup()
	lookup.add("mpg_disabled", store.APIKey{ID: "key-2", Enabled: false})
	a := NewAuthenticator(lookup, nil)
	r := newAuthTestRouter(a)

	w := doAuthRequest(r, "mpg_disabled", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("已停用的 API Key 期望 HTTP 401，实际 %d", w.Code)
	}
}

// TestMiddlewareRejectsExpiredKey 验证已过期的 API Key 返回 HTTP 401（Req 12.5、12.6）。
func TestMiddlewareRejectsExpiredKey(t *testing.T) {
	lookup := newFakeAuthLookup()
	// 设为过去时刻，确保鉴权时（time.Now()）已过期。
	lookup.add("mpg_expired", store.APIKey{ID: "key-3", Enabled: true, ExpiresAt: timePtr(time.Now().Add(-time.Hour))})
	a := NewAuthenticator(lookup, nil)
	r := newAuthTestRouter(a)

	w := doAuthRequest(r, "mpg_expired", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("已过期的 API Key 期望 HTTP 401，实际 %d", w.Code)
	}
}

// TestMiddlewareAllowsValidKeyAndInjectsContext 验证合法 API Key 放行且将元数据注入上下文（Req 11.9、12.5）。
func TestMiddlewareAllowsValidKeyAndInjectsContext(t *testing.T) {
	lookup := newFakeAuthLookup()
	// 未来有效期 + 启用：合法。
	lookup.add("mpg_good", store.APIKey{ID: "key-9", Enabled: true, ExpiresAt: timePtr(time.Now().Add(time.Hour))})
	a := NewAuthenticator(lookup, nil)
	r := newAuthTestRouter(a)

	// 分别用三种提取方式验证均可放行，且注入的 ID 正确。
	cases := []struct {
		name                  string
		header, bearer, query string
	}{
		{"X-API-Key 头", "mpg_good", "", ""},
		{"Authorization Bearer 头", "", "mpg_good", ""},
		{"api_key 查询参数", "", "", "mpg_good"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := doAuthRequest(r, c.header, c.bearer, c.query)
			if w.Code != http.StatusOK {
				t.Fatalf("合法 API Key 期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
			}
			if got := w.Body.String(); !contains(got, "key-9") {
				t.Errorf("响应体应回显注入的 API Key ID key-9，实际 %s", got)
			}
		})
	}
}
