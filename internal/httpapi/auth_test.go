package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/auth"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件验证受保护认证端点 GET /api/admin/auth/me：用户名取自 JWT 主体，经
// 鉴权中间件存入 gin.Context（auth.ClaimsFromContext 读取）。

// fakeTokenParser 是 auth.TokenParser 的内存实现：把任意令牌解析为固定会话信息，
// 令中间件按真实路径将 Claims 存入上下文，从而覆盖 /me 的 context-key 读取逻辑。
type fakeTokenParser struct {
	claims auth.Claims
	err    error
}

func (f *fakeTokenParser) ParseToken(string) (auth.Claims, error) {
	if f.err != nil {
		return auth.Claims{}, f.err
	}
	return f.claims, nil
}

// newAuthTestEngine 以真实的 auth.RequireAdmin 中间件挂载管理路由，便于验证
// 中间件→上下文→处理器的完整链路。
func newAuthTestEngine(d Deps, parser auth.TokenParser) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	NewRouter(d).Register(e, auth.RequireAdmin(parser))
	return e
}

// TestCurrentAdminReturnsUsernameFromContext 验证 /me 返回 JWT 主体对应的用户名。
func TestCurrentAdminReturnsUsernameFromContext(t *testing.T) {
	parser := &fakeTokenParser{claims: auth.Claims{Username: "alice", ExpiresAt: time.Now().Add(time.Hour)}}
	e := newAuthTestEngine(Deps{}, parser)

	r := httptest.NewRequest(http.MethodGet, "/api/admin/auth/me", nil)
	r.Header.Set("Authorization", "Bearer any-token")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	var got struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if got.Username != "alice" {
		t.Errorf("期望返回用户名 alice，实际 %q", got.Username)
	}
}

// TestCurrentAdminUnauthorizedWithoutToken 验证缺少令牌时被中间件拦截为 401。
func TestCurrentAdminUnauthorizedWithoutToken(t *testing.T) {
	parser := &fakeTokenParser{err: domain.NewError(domain.CodeUnauthorized, "令牌无效")}
	e := newAuthTestEngine(Deps{}, parser)

	// 不携带 Authorization 头：中间件在解析前即因缺少 Bearer 令牌而拒绝。
	w := doJSON(e, http.MethodGet, "/api/admin/auth/me", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("期望 HTTP 401，实际 %d", w.Code)
	}
}
