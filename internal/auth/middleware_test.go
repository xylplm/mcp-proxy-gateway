package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

func init() {
	// 测试中关闭 gin 的调试日志输出，保持测试输出干净。
	gin.SetMode(gin.TestMode)
}

// newTestRouter 构造一个挂载了 RequireAdmin 中间件的路由，受保护处理器在通过
// 鉴权后将上下文中的 Claims 主体写回响应体，便于断言 Claims 是否正确注入。
func newTestRouter(parser TokenParser) *gin.Engine {
	r := gin.New()
	r.GET("/api/admin/ping", RequireAdmin(parser), func(c *gin.Context) {
		claims, ok := ClaimsFromContext(c)
		if !ok {
			// 通过中间件后理应能取到 Claims；取不到说明中间件未正确注入。
			c.JSON(http.StatusInternalServerError, gin.H{"error": "missing claims"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"username": claims.Username})
	})
	return r
}

// newTestService 构造一个带固定时钟、已注册管理员的认证服务，便于签发可控令牌。
func newTestService(t *testing.T, sessionTimeoutS int, clock func() time.Time) *Service {
	t.Helper()
	store := newFakeStore()
	store.cfg.Auth.SessionTimeoutS = sessionTimeoutS
	svc, err := New(store, []byte("test-signing-key-0123456789abcdef"))
	if err != nil {
		t.Fatalf("New 不应返回错误：%v", err)
	}
	if clock != nil {
		svc.now = clock
	}
	if err := svc.Register("admin", "password123"); err != nil {
		t.Fatalf("注册失败：%v", err)
	}
	return svc
}

// decodeAPIError 将响应体解析为 domain.APIError 以断言统一错误模型。
func decodeAPIError(t *testing.T, body []byte) domain.APIError {
	t.Helper()
	var apiErr domain.APIError
	if err := json.Unmarshal(body, &apiErr); err != nil {
		t.Fatalf("响应体应为 APIError JSON，解析失败：%v，原始：%s", err, string(body))
	}
	return apiErr
}

// TestRequireAdminAllowsValidToken 验证携带有效令牌的请求被放行且 Claims 注入上下文（Req 1.6）。
func TestRequireAdminAllowsValidToken(t *testing.T) {
	svc := newTestService(t, config.DefaultYAMLConfig().Auth.SessionTimeoutS, nil)
	token, _, err := svc.Login("admin", "password123")
	if err != nil {
		t.Fatalf("登录失败：%v", err)
	}

	router := newTestRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/ping", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("有效令牌应放行，期望 200，实际 %d，响应：%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if body.Username != "admin" {
		t.Errorf("期望上下文 Claims 主体为 admin，实际 %q", body.Username)
	}
}

// TestRequireAdminRejectsMissingHeader 验证缺失 Authorization 头时返回 401（Req 1.6）。
func TestRequireAdminRejectsMissingHeader(t *testing.T) {
	svc := newTestService(t, config.DefaultYAMLConfig().Auth.SessionTimeoutS, nil)
	router := newTestRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/ping", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("缺失令牌应返回 401，实际 %d", rec.Code)
	}
	apiErr := decodeAPIError(t, rec.Body.Bytes())
	if apiErr.Code != domain.CodeUnauthorized {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeUnauthorized, apiErr.Code)
	}
}

// TestRequireAdminRejectsMalformedHeader 验证 Authorization 头格式不符时返回 401（Req 1.6）。
func TestRequireAdminRejectsMalformedHeader(t *testing.T) {
	svc := newTestService(t, config.DefaultYAMLConfig().Auth.SessionTimeoutS, nil)
	token, _, err := svc.Login("admin", "password123")
	if err != nil {
		t.Fatalf("登录失败：%v", err)
	}

	cases := []struct {
		name   string
		header string
	}{
		{"无 Bearer 前缀", token},
		{"错误方案", "Basic " + token},
		{"仅 Bearer 无令牌", "Bearer "},
		{"Bearer 后仅空白", "Bearer    "},
		{"空字符串", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			router := newTestRouter(svc)
			req := httptest.NewRequest(http.MethodGet, "/api/admin/ping", nil)
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("格式不符应返回 401，实际 %d", rec.Code)
			}
			apiErr := decodeAPIError(t, rec.Body.Bytes())
			if apiErr.Code != domain.CodeUnauthorized {
				t.Errorf("期望错误码 %q，实际 %q", domain.CodeUnauthorized, apiErr.Code)
			}
		})
	}
}

// TestRequireAdminAcceptsCaseInsensitiveScheme 验证方案前缀不区分大小写（兼容性）。
func TestRequireAdminAcceptsCaseInsensitiveScheme(t *testing.T) {
	svc := newTestService(t, config.DefaultYAMLConfig().Auth.SessionTimeoutS, nil)
	token, _, err := svc.Login("admin", "password123")
	if err != nil {
		t.Fatalf("登录失败：%v", err)
	}

	router := newTestRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/ping", nil)
	req.Header.Set("Authorization", "bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("小写 bearer 前缀应被接受，期望 200，实际 %d", rec.Code)
	}
}

// TestRequireAdminRejectsInvalidSignature 验证签名不符的令牌被拒绝返回 401（Req 1.6）。
func TestRequireAdminRejectsInvalidSignature(t *testing.T) {
	svc := newTestService(t, config.DefaultYAMLConfig().Auth.SessionTimeoutS, nil)
	token, _, err := svc.Login("admin", "password123")
	if err != nil {
		t.Fatalf("登录失败：%v", err)
	}

	// 使用不同签名密钥的服务作为校验方，签名应无法通过。
	other, err := New(newFakeStore(), []byte("a-completely-different-signing-key"))
	if err != nil {
		t.Fatalf("New 失败：%v", err)
	}
	router := newTestRouter(other)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/ping", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("签名不符应返回 401，实际 %d", rec.Code)
	}
	apiErr := decodeAPIError(t, rec.Body.Bytes())
	if apiErr.Code != domain.CodeUnauthorized {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeUnauthorized, apiErr.Code)
	}
}

// TestRequireAdminRejectsExpiredToken 验证超过会话超时的过期令牌被拒绝返回 401（Req 1.7）。
func TestRequireAdminRejectsExpiredToken(t *testing.T) {
	// 在过去签发短期令牌，使其在当前时刻已过期。
	past := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := newTestService(t, 300, func() time.Time { return past })
	token, _, err := svc.Login("admin", "password123")
	if err != nil {
		t.Fatalf("登录失败：%v", err)
	}

	// 将服务时钟推进到超过会话超时之后，令牌应失效。
	svc.now = func() time.Time { return past.Add(2 * time.Hour) }

	router := newTestRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/ping", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("过期令牌应返回 401，实际 %d", rec.Code)
	}
	apiErr := decodeAPIError(t, rec.Body.Bytes())
	if apiErr.Code != domain.CodeUnauthorized {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeUnauthorized, apiErr.Code)
	}
}

// TestRequireAdminRejectsNilParser 验证 parser 为 nil 时拒绝所有请求（防止误暴露）。
func TestRequireAdminRejectsNilParser(t *testing.T) {
	router := newTestRouter(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/ping", nil)
	req.Header.Set("Authorization", "Bearer something")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("parser 为 nil 时应返回 401，实际 %d", rec.Code)
	}
}

// TestClaimsFromContextWithoutMiddleware 验证未经中间件注入时读取 Claims 返回 false。
func TestClaimsFromContextWithoutMiddleware(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if _, ok := ClaimsFromContext(c); ok {
		t.Error("未注入 Claims 时应返回 ok=false")
	}
}
