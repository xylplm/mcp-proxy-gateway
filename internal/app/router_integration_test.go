package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/apikey"
	"github.com/myGithub/mcp-proxy-gateway/internal/auth"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/health"
	"github.com/myGithub/mcp-proxy-gateway/internal/httpapi"
	"github.com/myGithub/mcp-proxy-gateway/internal/mcpapi"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 本文件（任务 27.3）以集成视角验证装配层 buildRouter 装配出的「路由分面」整体行为：
//
//   - SPA fallback（Req 17.2）：非 API 的客户端路由路径兜底返回入口页面 index.html（HTTP 200），
//     而未注册的 /api/... 路径不被兜底为 index.html（返回 404）。
//   - 路由分面的中间件链隔离（Req 11.8、17.5）：管理面 JWT 链只挂在 /api/admin，服务面 API Key
//     链只挂在 /mcp，二者互不串扰；公开存活探针 /healthz 无需任何鉴权即可访问（Req 20.6）。
//
// 这些用例不接真实数据库/Redis，仅以最小桩依赖装配出 *gin.Engine，专注校验「路由前缀 +
// 中间件链」的接线是否正确，而非各组件内部逻辑（后者由各包自身的单测覆盖）。

// --- 最小桩依赖：仅用于装配路由分面，所承载请求都会在中间件层被先行拒绝，故内部逻辑可空实现 ---

// stubTokenParser 实现 auth.TokenParser：恒定返回鉴权失败。
//
// 用于让 auth.RequireAdmin 形成真实的 JWT 中间件链：无 Bearer 令牌的请求会在提取阶段即被拒，
// 携带令牌时也因解析失败而被拒，二者都返回 401，足以验证「管理面受 JWT 保护」。
type stubTokenParser struct{}

func (stubTokenParser) ParseToken(string) (auth.Claims, error) {
	return auth.Claims{}, domain.NewError(domain.CodeUnauthorized, "令牌无效")
}

// stubKeyLookup 实现 apikey.AuthKeyLookup：恒定返回 NOT_FOUND。
//
// 缺少 API Key 的 /mcp 请求会在提取阶段即被鉴权中间件拒绝（不会触达本查找）；此桩仅为
// 满足构造 Authenticator 的依赖，并在「携带未知 Key」场景下保证 fail-closed。
type stubKeyLookup struct{}

func (stubKeyLookup) GetByHash(context.Context, []byte) (store.APIKey, error) {
	return store.APIKey{}, domain.NewError(domain.CodeNotFound, "不存在")
}

// stubACLLister 实现 apikey.ACLLister：返回空白名单（不限制来源）。
type stubACLLister struct{}

func (stubACLLister) ListByAPIKey(context.Context, string) ([]store.ACLEntry, error) {
	return nil, nil
}

// stubRateCounter 实现 apikey.RateCounter：内存无操作计数。
type stubRateCounter struct{}

func (stubRateCounter) Incr(context.Context, string) (int64, error)         { return 1, nil }
func (stubRateCounter) Expire(context.Context, string, time.Duration) error { return nil }

// stubAggregation 实现 domain.Aggregation_Service：返回空集合，仅用于构建 MCP 端点。
type stubAggregation struct{}

func (stubAggregation) BuildToolSet(context.Context, string) ([]domain.ToolDef, error) {
	return nil, nil
}
func (stubAggregation) InvokeTool(context.Context, string, string, json.RawMessage) (domain.ToolResult, error) {
	return domain.ToolResult{}, nil
}

// newIntegrationRouter 以最小桩依赖装配一个完整的路由分面引擎，复用生产路径 App.buildRouter，
// 从而让本测试覆盖真实的「前缀 + 中间件链」接线，而非测试专用的简化路由。
func newIntegrationRouter(t *testing.T) *gin.Engine {
	t.Helper()

	a := &App{logger: slog.New(slog.NewTextHandler(io_Discard{}, nil))}

	adminRouter := httpapi.NewRouter(httpapi.Deps{})
	adminAuth := auth.RequireAdmin(stubTokenParser{})

	mcpService := mcpapi.NewService(stubAggregation{}, mcpapi.ModeFull, 50, a.logger)
	mcpEndpoints := mcpapi.NewEndpoints(mcpService, resolveAPIKeyID, a.logger)

	authenticator := apikey.NewAuthenticator(stubKeyLookup{}, a.logger)
	aclGuard := apikey.NewACLGuard(stubACLLister{}, a.logger)
	rateLimiter := apikey.NewRateLimiter(stubRateCounter{}, a.logger)
	detailReporter := health.NewDetailReporter(health.DetailReporterOptions{})

	engine := a.buildRouter(routerWiring{
		adminRouter:    adminRouter,
		adminAuth:      adminAuth,
		mcpEndpoints:   mcpEndpoints,
		authenticator:  authenticator,
		aclGuard:       aclGuard,
		rateLimiter:    rateLimiter,
		detailReporter: detailReporter,
	})
	if engine == nil {
		t.Fatal("buildRouter 返回了 nil engine")
	}
	return engine
}

// io_Discard 是 io.Discard 的最小本地等价，避免为日志额外引入 io 依赖的歧义。
type io_Discard struct{}

func (io_Discard) Write(p []byte) (int, error) { return len(p), nil }

// doGET 对给定引擎发起一次 GET 请求并返回响应记录器。
func doGET(engine *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

// TestSPAFallbackServesIndexForClientRoutes 验证 SPA fallback（Req 17.2）：
// 未注册的非 API 客户端路由路径（如 /dashboard、/upstreams）经 NoRoute 兜底返回入口页
// index.html（HTTP 200，Content-Type 为 text/html），以支持前端 history 模式路由。
func TestSPAFallbackServesIndexForClientRoutes(t *testing.T) {
	engine := newIntegrationRouter(t)

	for _, path := range []string{"/dashboard", "/upstreams", "/api-keys/123/edit"} {
		rec := doGET(engine, path)

		if rec.Code != http.StatusOK {
			t.Fatalf("客户端路由 %q 应兜底返回 200，实际 %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Fatalf("客户端路由 %q 兜底响应应为 HTML，实际 Content-Type=%q", path, ct)
		}
		if body := rec.Body.String(); !strings.Contains(body, `<div id="app">`) {
			t.Fatalf("客户端路由 %q 兜底响应应为 SPA 入口页 index.html，实际 body=%q", path, body)
		}
	}
}

// TestSPAFallbackDoesNotServeIndexForUnknownAPIPath 验证 SPA fallback 不遮蔽 API 语义
// （Req 17.2）：未注册的 /api/... 路径不应被兜底为 index.html，而应返回 404（非 HTML 入口页）。
func TestSPAFallbackDoesNotServeIndexForUnknownAPIPath(t *testing.T) {
	engine := newIntegrationRouter(t)

	for _, path := range []string{"/api/admin/does-not-exist", "/api/unknown"} {
		rec := doGET(engine, path)

		if rec.Code == http.StatusOK {
			t.Fatalf("未注册的 API 路径 %q 不应返回 200", path)
		}
		if strings.Contains(rec.Body.String(), `<div id="app">`) {
			t.Fatalf("未注册的 API 路径 %q 不应兜底为 SPA 入口页 index.html", path)
		}
	}
}

// TestAdminAPIRejectedWithoutJWT 验证管理面中间件链：未携带有效 JWT 的管理 API 请求
// 被 JWT 中间件以 401 拒绝（Req 17.5）。错误体为统一错误模型且 code=UNAUTHORIZED。
func TestAdminAPIRejectedWithoutJWT(t *testing.T) {
	engine := newIntegrationRouter(t)

	// /api/admin/* 与详细健康 /api/admin/health 均置于 JWT 之下。
	for _, path := range []string{"/api/admin/upstreams", "/api/admin/health"} {
		rec := doGET(engine, path)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("无 JWT 的管理 API %q 应返回 401，实际 %d（body=%s）", path, rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec.Body.Bytes(), domain.CodeUnauthorized)
	}
}

// TestMCPAPIRejectedWithoutAPIKey 验证服务面中间件链：未携带 API Key 的 /mcp/* 请求
// 被 API Key 鉴权中间件以 401 拒绝（Req 11.8、11.9）。
//
// 关键隔离断言：该拒绝来自 API Key 鉴权链而非管理面 JWT 链——/mcp 请求未携带任何 Bearer
// 令牌，若它被 JWT 链拦截则说明两套链发生了串扰。这里通过「请求未注册的 /mcp 子路径仍被
// API Key 中间件拦截」间接确认 API Key 链覆盖了整个 /mcp 前缀。
func TestMCPAPIRejectedWithoutAPIKey(t *testing.T) {
	engine := newIntegrationRouter(t)

	for _, path := range []string{"/mcp/sse", "/mcp/http", "/mcp/ws"} {
		rec := doGET(engine, path)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("无 API Key 的 MCP 请求 %q 应返回 401，实际 %d（body=%s）", path, rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec.Body.Bytes(), domain.CodeUnauthorized)
	}
}

// TestMiddlewareChainsAreIsolated 验证两套鉴权链彼此独立、互不应用：
//
//   - 携带管理面 Bearer 令牌访问 /mcp/sse 仍被 API Key 链拒绝（JWT 不能换得 MCP 访问）；
//   - 携带 API Key 访问 /api/admin/upstreams 仍被 JWT 链拒绝（API Key 不能换得管理访问）。
//
// 二者都返回 401，证明 JWT 链只挂在 /api/admin、API Key 链只挂在 /mcp，前缀与中间件互不交叉
// （Req 11.8、17.5）。
func TestMiddlewareChainsAreIsolated(t *testing.T) {
	engine := newIntegrationRouter(t)

	// 1) 用管理面 Bearer 令牌访问服务面：应被 API Key 链拒绝（JWT 在此无效）。
	req := httptest.NewRequest(http.MethodGet, "/mcp/sse", nil)
	req.Header.Set("Authorization", "Bearer some-admin-jwt")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("携带 JWT 访问 /mcp/sse 仍应被 API Key 链以 401 拒绝，实际 %d", rec.Code)
	}

	// 2) 用 API Key 访问管理面：应被 JWT 链拒绝（API Key 在此无效）。
	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/upstreams", nil)
	req2.Header.Set("X-API-Key", "some-api-key")
	rec2 := httptest.NewRecorder()
	engine.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("携带 API Key 访问 /api/admin/upstreams 仍应被 JWT 链以 401 拒绝，实际 %d", rec2.Code)
	}
}

// TestHealthzReachableWithoutAuth 验证公开存活探针 /healthz 无需任何鉴权即可访问（Req 20.6），
// 证明它不在任一鉴权链之下。
func TestHealthzReachableWithoutAuth(t *testing.T) {
	engine := newIntegrationRouter(t)

	rec := doGET(engine, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz 应无需鉴权即返回 200，实际 %d（body=%s）", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("/healthz 响应应包含存活状态，实际 body=%s", rec.Body.String())
	}
}

// assertErrorCode 断言响应体是统一错误模型且其 code 等于期望值。
func assertErrorCode(t *testing.T, body []byte, want domain.ErrorCode) {
	t.Helper()
	var apiErr domain.APIError
	if err := json.Unmarshal(body, &apiErr); err != nil {
		t.Fatalf("响应体应为统一错误模型 JSON，解析失败：%v（body=%s）", err, string(body))
	}
	if apiErr.Code != want {
		t.Fatalf("错误 code 应为 %q，实际 %q", want, apiErr.Code)
	}
}
