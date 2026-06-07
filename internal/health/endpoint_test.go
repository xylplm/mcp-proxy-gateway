package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

func init() {
	// 测试期使用 gin 的安静模式，避免调试日志污染测试输出。
	gin.SetMode(gin.TestMode)
}

// fakeXiaoZhiStatus 是 XiaoZhiStatusProvider 的内存实现。
type fakeXiaoZhiStatus struct {
	enabled  bool
	endpoint string
	running  bool
}

func (f fakeXiaoZhiStatus) Enabled() bool    { return f.enabled }
func (f fakeXiaoZhiStatus) Endpoint() string { return f.endpoint }
func (f fakeXiaoZhiStatus) Running() bool    { return f.running }

// upstreamWithState 构造带连接状态的上游领域对象。
func upstreamWithState(id, name string, enabled bool, state domain.ConnState, lastErr string) domain.Upstream {
	return domain.Upstream{
		ID:        id,
		Config:    domain.UpstreamConfig{Name: name, Enabled: enabled},
		State:     state,
		LastError: lastErr,
	}
}

// --- 公开存活探针 /healthz（Req 20.6）---

func TestLiveness_ReturnsOKWithoutAuth(t *testing.T) {
	router := gin.New()
	Register(router, nil, nil) // 无鉴权中间件、无 reporter：仅注册 /healthz。

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz 应返回 200，实际 %d", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应应为合法 JSON：%v（body=%s）", err, rec.Body.String())
	}
	if body["status"] != StatusOK {
		t.Errorf("status 应为 %q，实际 %v", StatusOK, body["status"])
	}
}

// /healthz 不得泄露任何依赖/上游/小智明细（Req 20.6）。
func TestLiveness_DoesNotLeakDependencyDetails(t *testing.T) {
	router := gin.New()
	Register(router, nil, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	router.ServeHTTP(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应应为合法 JSON：%v", err)
	}
	for _, k := range []string{"dependencies", "upstreams", "xiaozhi", "postgres", "redis"} {
		if _, ok := body[k]; ok {
			t.Errorf("/healthz 不应包含字段 %q，实际响应：%s", k, rec.Body.String())
		}
	}
	if len(body) != 1 {
		t.Errorf("/healthz 响应应仅含 status 一个字段，实际：%s", rec.Body.String())
	}
}

// --- 详细健康端点 /api/admin/health 鉴权（Req 20.8）---

// allowAuth 模拟通过鉴权的中间件（放行）。
func allowAuth() gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}

// denyAuth 模拟未通过鉴权的中间件：返回 401 UNAUTHORIZED 并中止（与 auth.RequireAdmin 语义一致）。
func denyAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, domain.NewError(domain.CodeUnauthorized, "令牌无效或已过期"))
	}
}

func TestDetailHealth_UnauthorizedReturns401(t *testing.T) {
	router := gin.New()
	reporter := NewDetailReporter(DetailReporterOptions{})
	Register(router, denyAuth(), reporter)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未鉴权应返回 401，实际 %d", rec.Code)
	}
	var apiErr domain.APIError
	if err := json.Unmarshal(rec.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("响应应为合法 APIError JSON：%v", err)
	}
	if apiErr.Code != domain.CodeUnauthorized {
		t.Errorf("错误码应为 %q，实际 %q", domain.CodeUnauthorized, apiErr.Code)
	}
}

// 未通过鉴权时不应执行 reporter（不泄露明细，Req 20.8）。
func TestDetailHealth_UnauthorizedDoesNotInvokeReporter(t *testing.T) {
	router := gin.New()
	called := false
	reporter := NewDetailReporter(DetailReporterOptions{
		ListUpstreams: func(_ context.Context) ([]domain.Upstream, error) {
			called = true
			return nil, nil
		},
	})
	Register(router, denyAuth(), reporter)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	router.ServeHTTP(rec, req)

	if called {
		t.Error("未通过鉴权时不应执行健康汇总逻辑")
	}
}

func TestDetailHealth_AuthorizedReturnsDetails(t *testing.T) {
	router := gin.New()
	reporter := NewDetailReporter(DetailReporterOptions{
		Pinger: fakePinger{},
		ListUpstreams: func(_ context.Context) ([]domain.Upstream, error) {
			return []domain.Upstream{
				upstreamWithState("up-a", "上游A", true, domain.ConnAvailable, ""),
			}, nil
		},
		XiaoZhi: fakeXiaoZhiStatus{enabled: true, endpoint: "wss://xz.example/mcp", running: true},
	})
	Register(router, allowAuth(), reporter)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("通过鉴权应返回 200，实际 %d（body=%s）", rec.Code, rec.Body.String())
	}

	var report DetailReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("响应应为合法 DetailReport JSON：%v", err)
	}
	if report.Status != StatusOK {
		t.Errorf("全部健康时整体状态应为 %q，实际 %q", StatusOK, report.Status)
	}
	if len(report.Dependencies) != 2 {
		t.Errorf("应含 PG/Redis 两项依赖，实际 %d 项：%+v", len(report.Dependencies), report.Dependencies)
	}
	if len(report.Upstreams) != 1 || report.Upstreams[0].ID != "up-a" {
		t.Errorf("应含 1 个上游明细，实际：%+v", report.Upstreams)
	}
	if report.XiaoZhi == nil || !report.XiaoZhi.Connected {
		t.Errorf("应含已连接的小智明细，实际：%+v", report.XiaoZhi)
	}
}

// --- DetailReporter.Report 状态判定 ---

func TestReport_AllHealthyIsOK(t *testing.T) {
	reporter := NewDetailReporter(DetailReporterOptions{
		Pinger: fakePinger{},
		ListUpstreams: func(_ context.Context) ([]domain.Upstream, error) {
			return []domain.Upstream{
				upstreamWithState("up-a", "A", true, domain.ConnAvailable, ""),
				// 停用上游即便不可用也不应导致降级。
				upstreamWithState("up-b", "B", false, domain.ConnUnavailable, "停用"),
			}, nil
		},
	})

	report := reporter.Report(context.Background())

	if report.Status != StatusOK {
		t.Errorf("整体应为 ok，实际 %q", report.Status)
	}
}

func TestReport_DependencyFailureIsDegraded(t *testing.T) {
	pgErr := errors.New("dial timeout")
	reporter := NewDetailReporter(DetailReporterOptions{
		Pinger: fakePinger{pgErr: pgErr},
	})

	report := reporter.Report(context.Background())

	if report.Status != StatusDegraded {
		t.Errorf("依赖失败时整体应为 degraded，实际 %q", report.Status)
	}
	var pg DependencyHealth
	for _, d := range report.Dependencies {
		if d.Name == "PostgreSQL" {
			pg = d
		}
	}
	if pg.Status != StatusFailed || !strings.Contains(pg.Reason, "dial timeout") {
		t.Errorf("PG 应记录失败原因，实际 %+v", pg)
	}
}

func TestReport_EnabledUpstreamUnavailableIsDegraded(t *testing.T) {
	reporter := NewDetailReporter(DetailReporterOptions{
		ListUpstreams: func(_ context.Context) ([]domain.Upstream, error) {
			return []domain.Upstream{
				upstreamWithState("up-a", "A", true, domain.ConnSuspended, "握手失败"),
			}, nil
		},
	})

	report := reporter.Report(context.Background())

	if report.Status != StatusDegraded {
		t.Errorf("启用上游不可用时整体应为 degraded，实际 %q", report.Status)
	}
	if len(report.Upstreams) != 1 || report.Upstreams[0].LastError != "握手失败" {
		t.Errorf("应透传上游最近失败原因，实际：%+v", report.Upstreams)
	}
}

func TestReport_XiaoZhiEnabledNotConnectedIsDegraded(t *testing.T) {
	reporter := NewDetailReporter(DetailReporterOptions{
		XiaoZhi: fakeXiaoZhiStatus{enabled: true, endpoint: "wss://xz.example/mcp", running: false},
	})

	report := reporter.Report(context.Background())

	if report.Status != StatusDegraded {
		t.Errorf("小智启用但未连接时整体应为 degraded，实际 %q", report.Status)
	}
}

func TestReport_XiaoZhiDisabledIsOK(t *testing.T) {
	reporter := NewDetailReporter(DetailReporterOptions{
		XiaoZhi: fakeXiaoZhiStatus{enabled: false},
	})

	report := reporter.Report(context.Background())

	if report.Status != StatusOK {
		t.Errorf("小智未启用时整体应为 ok，实际 %q", report.Status)
	}
	if report.XiaoZhi == nil || report.XiaoZhi.Enabled {
		t.Errorf("应含 enabled=false 的小智明细，实际：%+v", report.XiaoZhi)
	}
}

func TestReport_UpstreamListErrorIsDegraded(t *testing.T) {
	reporter := NewDetailReporter(DetailReporterOptions{
		ListUpstreams: func(_ context.Context) ([]domain.Upstream, error) {
			return nil, errors.New("查询上游列表失败")
		},
	})

	report := reporter.Report(context.Background())

	if report.Status != StatusDegraded {
		t.Errorf("上游列表查询失败时整体应为 degraded，实际 %q", report.Status)
	}
	found := false
	for _, d := range report.Dependencies {
		if d.Name == "upstream-list" && d.Status == StatusFailed {
			found = true
		}
	}
	if !found {
		t.Errorf("应记录 upstream-list 失败项，实际：%+v", report.Dependencies)
	}
}

// 缺省全部依赖能力时整体为 ok，且不含任何依赖/上游/小智段。
func TestReport_NoProbesIsOK(t *testing.T) {
	reporter := NewDetailReporter(DetailReporterOptions{})

	report := reporter.Report(context.Background())

	if report.Status != StatusOK {
		t.Errorf("无任何探测项时应为 ok，实际 %q", report.Status)
	}
	if len(report.Dependencies) != 0 || len(report.Upstreams) != 0 || report.XiaoZhi != nil {
		t.Errorf("无注入能力时不应有明细，实际：%+v", report)
	}
}

// 缺省 adminAuth 时不注册详细端点，避免无保护暴露（Req 20.8）。
func TestRegister_NilAuthSkipsDetailEndpoint(t *testing.T) {
	router := gin.New()
	reporter := NewDetailReporter(DetailReporterOptions{})
	Register(router, nil, reporter)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("缺省鉴权时详细端点不应注册（应 404），实际 %d", rec.Code)
	}
}
