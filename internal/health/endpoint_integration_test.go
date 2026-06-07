package health_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/auth"
	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/health"
)

// 本文件（任务 22.3）编写健康端点的集成测试：以「真实」的管理员鉴权中间件
// （auth.RequireAdmin 搭配真实 auth.Service 与 JWT 签发/校验）经 health.Register
// 装配 /healthz 与 /api/admin/health，验证三点端到端行为（Req 20.6、20.7、20.8）：
//
//   - 公开存活探针 /healthz 无需鉴权，仅返回自身存活状态，绝不泄露任何依赖/上游/
//     小智连接明细（Req 20.6）。
//   - 详细端点 /api/admin/health 携带有效管理员 JWT 时返回系统整体状态与各依赖、
//     上游、小智连接明细（Req 20.7）。
//   - 详细端点在缺失或无效令牌时被鉴权中间件拒绝并返回 401 UNAUTHORIZED，且不执行
//     健康汇总逻辑（不泄露明细，Req 20.8）。
//
// 与 endpoint_test.go 中以 allowAuth/denyAuth 桩模拟鉴权不同，本集成测试贯穿真实
// 的令牌签发与校验链路，验证 health 包与 auth 包接线的整体正确性。

func init() {
	// 集成测试同样使用 gin 安静模式，避免调试日志污染输出。
	gin.SetMode(gin.TestMode)
}

// integrationPinger 是 health.Pinger 的内存实现，PG/Redis 均探测成功。
type integrationPinger struct{}

func (integrationPinger) PingPG(_ context.Context) error    { return nil }
func (integrationPinger) PingRedis(_ context.Context) error { return nil }

// integrationXiaoZhi 是 health.XiaoZhiStatusProvider 的内存实现，模拟已连接的小智接入。
type integrationXiaoZhi struct{}

func (integrationXiaoZhi) Enabled() bool    { return true }
func (integrationXiaoZhi) Endpoint() string { return "wss://xz.example/mcp" }
func (integrationXiaoZhi) Running() bool    { return true }

// integrationStore 是 auth.ConfigStore 的内存实现，承载管理员凭证与会话超时配置。
type integrationStore struct {
	cfg config.YAMLConfig
}

func (s *integrationStore) Config() config.YAMLConfig { return s.cfg }

func (s *integrationStore) Save(cfg config.YAMLConfig) error {
	if err := config.ValidateYAMLConfig(cfg); err != nil {
		return err
	}
	s.cfg = cfg
	return nil
}

// newRealAuthService 构造一个已注册管理员的真实认证服务，并返回该管理员的明文凭证。
func newRealAuthService(t *testing.T) (*auth.Service, string, string) {
	t.Helper()
	store := &integrationStore{cfg: config.DefaultYAMLConfig()}
	svc, err := auth.New(store, []byte("integration-signing-key-0123456789abcdef"))
	if err != nil {
		t.Fatalf("构造认证服务不应失败：%v", err)
	}
	const username, password = "admin", "password123"
	if err := svc.Register(username, password); err != nil {
		t.Fatalf("注册管理员不应失败：%v", err)
	}
	return svc, username, password
}

// newDetailReporterWithDetails 构造一个含 PG/Redis、上游、小智明细的详细健康汇总器。
func newDetailReporterWithDetails() *health.DetailReporter {
	return health.NewDetailReporter(health.DetailReporterOptions{
		Pinger: integrationPinger{},
		ListUpstreams: func(_ context.Context) ([]domain.Upstream, error) {
			return []domain.Upstream{
				{
					ID:     "up-a",
					Config: domain.UpstreamConfig{Name: "上游A", Enabled: true},
					State:  domain.ConnAvailable,
				},
			}, nil
		},
		XiaoZhi: integrationXiaoZhi{},
	})
}

// buildRouter 以真实鉴权中间件与详细健康汇总器装配健康端点路由。
func buildRouter(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	svc, username, password := newRealAuthService(t)
	token, _, err := svc.Login(username, password)
	if err != nil {
		t.Fatalf("登录签发令牌不应失败：%v", err)
	}

	router := gin.New()
	health.Register(router, auth.RequireAdmin(svc), newDetailReporterWithDetails())
	return router, token
}

// --- Req 20.6：公开存活探针仅返回存活状态、不泄露明细 ---

// TestIntegration_Liveness_PublicAndMinimal 验证 /healthz 无需鉴权即可访问，
// 且响应仅含 status 一个字段，不泄露任何依赖/上游/小智明细（Req 20.6）。
func TestIntegration_Liveness_PublicAndMinimal(t *testing.T) {
	router, _ := buildRouter(t)

	rec := httptest.NewRecorder()
	// 不携带任何 Authorization 头，模拟探活探针的匿名访问。
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz 应无需鉴权返回 200，实际 %d", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应应为合法 JSON：%v（body=%s）", err, rec.Body.String())
	}
	if body["status"] != health.StatusOK {
		t.Errorf("status 应为 %q，实际 %v", health.StatusOK, body["status"])
	}
	// 不得泄露任何依赖/上游/小智明细字段（Req 20.6）。
	for _, k := range []string{"dependencies", "upstreams", "xiaozhi", "postgres", "redis"} {
		if _, ok := body[k]; ok {
			t.Errorf("/healthz 不应包含明细字段 %q，实际响应：%s", k, rec.Body.String())
		}
	}
	if len(body) != 1 {
		t.Errorf("/healthz 响应应仅含 status 一个字段，实际：%s", rec.Body.String())
	}
}

// --- Req 20.7：通过管理员鉴权后返回整体状态与各连接明细 ---

// TestIntegration_DetailHealth_AuthorizedReturnsDetails 验证携带有效管理员 JWT 时，
// 详细端点经真实鉴权中间件放行并返回系统整体状态与各依赖/上游/小智明细（Req 20.7）。
func TestIntegration_DetailHealth_AuthorizedReturnsDetails(t *testing.T) {
	router, token := buildRouter(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("携带有效令牌应返回 200，实际 %d（body=%s）", rec.Code, rec.Body.String())
	}

	var report health.DetailReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("响应应为合法 DetailReport JSON：%v", err)
	}
	if report.Status != health.StatusOK {
		t.Errorf("全部健康时整体状态应为 %q，实际 %q", health.StatusOK, report.Status)
	}
	if len(report.Dependencies) != 2 {
		t.Errorf("应含 PG/Redis 两项依赖明细，实际 %d 项：%+v", len(report.Dependencies), report.Dependencies)
	}
	if len(report.Upstreams) != 1 || report.Upstreams[0].ID != "up-a" {
		t.Errorf("应含 1 个上游连接明细，实际：%+v", report.Upstreams)
	}
	if report.XiaoZhi == nil || !report.XiaoZhi.Connected {
		t.Errorf("应含已连接的小智明细，实际：%+v", report.XiaoZhi)
	}
}

// --- Req 20.8：未通过鉴权返回 401 且不泄露明细 ---

// TestIntegration_DetailHealth_MissingTokenReturns401 验证缺失 Authorization 头时，
// 真实鉴权中间件拒绝请求并返回 401 UNAUTHORIZED（Req 20.8）。
func TestIntegration_DetailHealth_MissingTokenReturns401(t *testing.T) {
	router, _ := buildRouter(t)

	rec := httptest.NewRecorder()
	// 不携带 Authorization 头。
	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	router.ServeHTTP(rec, req)

	assertUnauthorizedNoDetails(t, rec)
}

// TestIntegration_DetailHealth_InvalidTokenReturns401 验证携带无效/伪造令牌时，
// 真实鉴权中间件校验失败并返回 401 UNAUTHORIZED（Req 20.8）。
func TestIntegration_DetailHealth_InvalidTokenReturns401(t *testing.T) {
	router, _ := buildRouter(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/health", nil)
	// 伪造一个签名无效的令牌。
	req.Header.Set("Authorization", "Bearer this.is.not-a-valid-jwt")
	router.ServeHTTP(rec, req)

	assertUnauthorizedNoDetails(t, rec)
}

// assertUnauthorizedNoDetails 断言响应为 401 且响应体为统一 UNAUTHORIZED 错误模型，
// 不含任何健康明细字段（Req 20.8）。
func assertUnauthorizedNoDetails(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未通过鉴权应返回 401，实际 %d（body=%s）", rec.Code, rec.Body.String())
	}

	var apiErr domain.APIError
	if err := json.Unmarshal(rec.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("响应应为合法 APIError JSON：%v", err)
	}
	if apiErr.Code != domain.CodeUnauthorized {
		t.Errorf("错误码应为 %q，实际 %q", domain.CodeUnauthorized, apiErr.Code)
	}

	// 401 响应不得泄露任何健康明细（Req 20.8）。
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应应为合法 JSON：%v", err)
	}
	for _, k := range []string{"dependencies", "upstreams", "xiaozhi", "status"} {
		if _, ok := body[k]; ok {
			t.Errorf("401 响应不应包含健康明细字段 %q，实际响应：%s", k, rec.Body.String())
		}
	}
}
