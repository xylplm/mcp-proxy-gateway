package httpapi

import (
	"errors"
	"net/http"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件以内存 fake 注入 SettingsService 与 CronValidator，验证系统设置读写端点的
// 路由装配、cron 专项校验、管理员凭证不外泄/不被篡改与错误映射。

// fakeSettings 是 SettingsService 的内存实现。
type fakeSettings struct {
	cfg     config.YAMLConfig
	saved   config.YAMLConfig
	saveErr error
}

func (f *fakeSettings) Config() config.YAMLConfig { return f.cfg }

func (f *fakeSettings) Save(cfg config.YAMLConfig) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved = cfg
	f.cfg = cfg
	return nil
}

// TestGetSettingsHidesAdminCredentials 验证读取设置不泄露管理员密码哈希（但保留初始化标志）。
func TestGetSettingsHidesAdminCredentials(t *testing.T) {
	s := &fakeSettings{cfg: config.YAMLConfig{
		Admin: config.AdminConfig{Username: "admin", PasswordHash: "secret-hash", Initialized: true},
		Sync:  config.SyncConfig{Cron: "0 */30 * * * *", TimeoutS: 30},
	}}
	e := newTestEngine(Deps{Settings: s})

	w := doJSON(e, http.MethodGet, "/api/admin/settings", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
	var got settingsResponse
	unmarshalData(t, w, &got)
	if got.Settings.Admin.PasswordHash != "" || got.Settings.Admin.Username != "" {
		t.Errorf("读取设置不应泄露管理员凭证：%+v", got.Settings.Admin)
	}
	if !got.Settings.Admin.Initialized {
		t.Errorf("应保留初始化标志")
	}
	if got.Settings.Sync.Cron != "0 */30 * * * *" {
		t.Errorf("常规配置应正常返回，实际 cron=%q", got.Settings.Sync.Cron)
	}
}

// TestUpdateSettingsValidCron 验证合法配置经 cron 校验后落盘，且管理员凭证沿用既有值。
func TestUpdateSettingsValidCron(t *testing.T) {
	s := &fakeSettings{cfg: config.YAMLConfig{
		Admin: config.AdminConfig{Username: "admin", PasswordHash: "keep-hash", Initialized: true},
	}}
	cronCalled := false
	validateCron := func(expr string) error { cronCalled = true; return nil }
	e := newTestEngine(Deps{Settings: s, ValidateCron: validateCron})

	body := `{"sync":{"cron":"0 0 * * * *","timeout_s":30},"mcp_api":{"mode":"smart","smart_discovery_limit":50}}`
	w := doJSON(e, http.MethodPut, "/api/admin/settings", body)
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if !cronCalled {
		t.Errorf("应对 cron 表达式做专项校验")
	}
	// 管理员凭证须沿用既有值，不被设置写入篡改。
	if s.saved.Admin.PasswordHash != "keep-hash" || s.saved.Admin.Username != "admin" {
		t.Errorf("更新设置不应改动管理员凭证：%+v", s.saved.Admin)
	}
	if s.saved.Sync.Cron != "0 0 * * * *" {
		t.Errorf("期望写入 cron=0 0 * * * *，实际 %q", s.saved.Sync.Cron)
	}
}

// TestUpdateSettingsInvalidCronRejected 验证非法 cron 返回 400 且不持久化（Req 7.3、7.4）。
func TestUpdateSettingsInvalidCronRejected(t *testing.T) {
	s := &fakeSettings{cfg: config.YAMLConfig{Admin: config.AdminConfig{Initialized: true}}}
	validateCron := func(expr string) error {
		return domain.NewValidationError("cron 非法", map[string]string{"cron": "格式错误"})
	}
	e := newTestEngine(Deps{Settings: s, ValidateCron: validateCron})

	body := `{"sync":{"cron":"not-a-cron","timeout_s":30}}`
	w := doJSON(e, http.MethodPut, "/api/admin/settings", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 cron 期望 HTTP 400，实际 %d", w.Code)
	}
	if s.saved.Sync.Cron != "" {
		t.Errorf("非法 cron 不应持久化，实际已写入 %q", s.saved.Sync.Cron)
	}
}

// TestUpdateSettingsWrapsPlainCronError 验证非 APIError 的 cron 错误被包装为字段级 400。
func TestUpdateSettingsWrapsPlainCronError(t *testing.T) {
	s := &fakeSettings{cfg: config.YAMLConfig{Admin: config.AdminConfig{Initialized: true}}}
	validateCron := func(expr string) error { return errors.New("解析失败") }
	e := newTestEngine(Deps{Settings: s, ValidateCron: validateCron})

	body := `{"sync":{"cron":"bad","timeout_s":30}}`
	w := doJSON(e, http.MethodPut, "/api/admin/settings", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 HTTP 400，实际 %d", w.Code)
	}
	code, _, fields := parseErrorEnvelope(t, w)
	if code != 40000 || fields["sync.cron"] == "" {
		t.Errorf("期望字段级 VALIDATION(40000) 错误指向 sync.cron，实际 code=%d fields=%+v", code, fields)
	}
}

// TestUpdateSettingsSaveErrorMapsStatus 验证配置层校验错误（如字段范围越界）被映射。
func TestUpdateSettingsSaveErrorMapsStatus(t *testing.T) {
	s := &fakeSettings{
		cfg:     config.YAMLConfig{Admin: config.AdminConfig{Initialized: true}},
		saveErr: domain.NewValidationError("会话超时越界", map[string]string{"auth.session_timeout_s": "范围 300-86400"}),
	}
	e := newTestEngine(Deps{Settings: s})

	body := `{"auth":{"session_timeout_s":1}}`
	w := doJSON(e, http.MethodPut, "/api/admin/settings", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("配置校验错误期望 HTTP 400，实际 %d", w.Code)
	}
}

// TestSettingsServiceUnavailable 验证依赖未接线时返回 503。
func TestSettingsServiceUnavailable(t *testing.T) {
	e := newTestEngine(Deps{})

	w := doJSON(e, http.MethodGet, "/api/admin/settings", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("依赖未接线期望 HTTP 503，实际 %d", w.Code)
	}
}
