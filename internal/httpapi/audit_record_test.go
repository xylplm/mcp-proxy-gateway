package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/audit"
	"github.com/myGithub/mcp-proxy-gateway/internal/auth"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件验证审计"写"链路接线：管理 handler 在主操作成功后是否正确提交审计事件。
//
// 使用内存 fakeAuditRecorder（实现 AuditRecorder 窄接口）记录被调用的方法与入参，
// 供各 handler 测试断言；与 audit_test.go 的 fakeAudit（查询）正交，互不干扰。

// fakeAuditRecorder 是 AuditRecorder 的内存实现，按调用顺序记录全部审计事件。
type fakeAuditRecorder struct {
	events     []recordedAuditEvent
	updateCtxs []context.Context
}

// recordedAuditEvent 记录一次审计调用的方法名与入参，便于断言。
type recordedAuditEvent struct {
	method   string // login/create/update/delete/access_denied
	username string
	success  bool
	kind     audit.ResourceKind
	target   string
	reason   string
}

func (f *fakeAuditRecorder) RecordLogin(_ context.Context, username string, success bool) error {
	f.events = append(f.events, recordedAuditEvent{method: "login", username: username, success: success})
	return nil
}

func (f *fakeAuditRecorder) RecordCreate(_ context.Context, kind audit.ResourceKind, target string) error {
	f.events = append(f.events, recordedAuditEvent{method: "create", kind: kind, target: target})
	return nil
}

func (f *fakeAuditRecorder) RecordUpdate(ctx context.Context, kind audit.ResourceKind, target string) error {
	f.events = append(f.events, recordedAuditEvent{method: "update", kind: kind, target: target})
	f.updateCtxs = append(f.updateCtxs, ctx)
	return nil
}

func (f *fakeAuditRecorder) RecordDelete(_ context.Context, kind audit.ResourceKind, target string) error {
	f.events = append(f.events, recordedAuditEvent{method: "delete", kind: kind, target: target})
	return nil
}

// fakeAuth 是 AuthService 的内存实现，供登录/改密端点测试使用。
type fakeAuth struct {
	initialized bool
	token       string
	expiresAt   time.Time
	err         error // Login/Register/ChangePassword 返回的预设错误。
}

func (f *fakeAuth) IsInitialized() bool { return f.initialized }
func (f *fakeAuth) Register(_, _ string) error {
	if f.err != nil {
		return f.err
	}
	return nil
}
func (f *fakeAuth) Login(_, _ string) (string, time.Time, error) {
	if f.err != nil {
		return "", time.Time{}, f.err
	}
	return f.token, f.expiresAt, nil
}
func (f *fakeAuth) ChangePassword(_, _ string) error {
	if f.err != nil {
		return f.err
	}
	return nil
}

// testExpiresAt 返回一个固定的未来过期时刻，供登录响应测试。
func testExpiresAt() time.Time {
	return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
}

// errUnauthorized 返回一个 UNAUTHORIZED 错误，供登录失败场景。
func errUnauthorized() error {
	return domain.NewError(domain.CodeUnauthorized, "凭证无效")
}

func (f *fakeAuditRecorder) RecordAccessDenied(_ context.Context, target, reason string) error {
	f.events = append(f.events, recordedAuditEvent{method: "access_denied", target: target, reason: reason})
	return nil
}

// newTestEngineWithRecorder 装配带审计记录器的测试引擎，并通过 claimInjector 注入会话
// （供改密等依赖 claims 的受保护端点测试）。
func newTestEngineWithRecorder(d Deps, claimInjector gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	NewRouter(d).Register(e, claimInjector)
	return e
}

// passThroughClaims 注入指定用户名的 claims，模拟已通过 RequireAdmin 鉴权。
func passThroughClaims(username string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("auth.claims", auth.Claims{Username: username})
		c.Next()
	}
}

// TestLoginSuccess_RecordsLoginAudit 验证登录成功提交 success=true 审计（Req 22.1）。
func TestLoginSuccess_RecordsLoginAudit(t *testing.T) {
	rec := &fakeAuditRecorder{}
	e := newTestEngineWithRecorder(Deps{
		Auth:          &fakeAuth{token: "tok", expiresAt: testExpiresAt()},
		AuditRecorder: rec,
	}, func(c *gin.Context) { c.Next() })

	w := doJSON(e, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"secret"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if len(rec.events) != 1 || rec.events[0].method != "login" || !rec.events[0].success || rec.events[0].username != "admin" {
		t.Fatalf("应记录 1 条 success=true 登录审计，实际 %+v", rec.events)
	}
}

// TestLoginFailure_RecordsFailedLoginAudit 验证登录失败提交 success=false 审计（Req 22.1）。
func TestLoginFailure_RecordsFailedLoginAudit(t *testing.T) {
	rec := &fakeAuditRecorder{}
	e := newTestEngineWithRecorder(Deps{
		Auth:          &fakeAuth{err: errUnauthorized()},
		AuditRecorder: rec,
	}, func(c *gin.Context) { c.Next() })

	w := doJSON(e, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"wrong"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("期望 HTTP 401，实际 %d", w.Code)
	}
	if len(rec.events) != 1 || rec.events[0].success {
		t.Fatalf("应记录 1 条 success=false 登录审计，实际 %+v", rec.events)
	}
}

func TestRecordUpdateUsesRequestContext(t *testing.T) {
	rec := &fakeAuditRecorder{}
	e := gin.New()
	e.GET("/touch", func(c *gin.Context) {
		NewRouter(Deps{AuditRecorder: rec}).recordUpdate(c, audit.ResourceSetting, "touch")
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/touch", nil)
	reqCtx := req.Context()
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("期望 HTTP 204，实际 %d", w.Code)
	}
	if len(rec.updateCtxs) != 1 || rec.updateCtxs[0] != reqCtx {
		t.Fatalf("recordUpdate 应透传请求上下文，实际 %+v", rec.updateCtxs)
	}
}
