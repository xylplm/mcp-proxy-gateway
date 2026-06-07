package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// setRequiredEnv 为测试设置全部必需环境变量为合法值。
//
// 使用 t.Setenv 设置，测试结束后会自动恢复原值，避免污染其他用例。
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MPG_PG_DSN", "postgres://user:pass@localhost:5432/mpg?sslmode=disable")
	t.Setenv("MPG_REDIS_ADDR", "localhost:6379")
	t.Setenv("MPG_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")
}

// asAPIError 将 error 断言为 *domain.APIError，失败则终止用例。
func asAPIError(t *testing.T, err error) *domain.APIError {
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

// TestLoadCreatesDefaultConfigWhenMissing 验证 dataDir 下不存在 config.yaml 时，
// Load 以默认配置创建该文件并返回与默认配置一致的结果（Req 18.5）。
func TestLoadCreatesDefaultConfigWhenMissing(t *testing.T) {
	setRequiredEnv(t)
	dataDir := t.TempDir()

	mgr, err := Load(nil, dataDir)
	if err != nil {
		t.Fatalf("Load 不应返回错误：%v", err)
	}

	// 应在 dataDir 下创建出 config.yaml 文件。
	yamlPath := filepath.Join(dataDir, "config.yaml")
	if _, statErr := os.Stat(yamlPath); statErr != nil {
		t.Fatalf("期望已创建默认配置文件 %s，但 Stat 失败：%v", yamlPath, statErr)
	}
	if mgr.YAMLPath() != yamlPath {
		t.Errorf("YAMLPath 不一致：期望 %q，实际 %q", yamlPath, mgr.YAMLPath())
	}

	// 返回与内存快照均应等于默认配置。
	want := DefaultYAMLConfig()
	if got := mgr.Config(); got != want {
		t.Errorf("默认配置不一致：\n期望 %+v\n实际 %+v", want, got)
	}
}

// TestLoadReadsExistingConfigAndAppliesDefaults 验证已存在的部分 YAML 能被读取，
// 且文件中缺省的字段沿用默认值（Req 18.2）。
func TestLoadReadsExistingConfigAndAppliesDefaults(t *testing.T) {
	setRequiredEnv(t)
	dataDir := t.TempDir()
	yamlPath := filepath.Join(dataDir, "config.yaml")

	// 仅写入部分字段，其余字段应回退到默认值。
	content := "auth:\n  session_timeout_s: 7200\n"
	if err := os.WriteFile(yamlPath, []byte(content), 0o644); err != nil {
		t.Fatalf("写入测试 YAML 失败：%v", err)
	}

	mgr, err := Load(nil, dataDir)
	if err != nil {
		t.Fatalf("Load 不应返回错误：%v", err)
	}

	got := mgr.Config()
	if got.Auth.SessionTimeoutS != 7200 {
		t.Errorf("auth.session_timeout_s 期望 7200，实际 %d", got.Auth.SessionTimeoutS)
	}
	// 未在文件中出现的字段应沿用默认值。
	if got.MCPAPI.Mode != ModeSmart {
		t.Errorf("mcp_api.mode 期望默认值 %q，实际 %q", ModeSmart, got.MCPAPI.Mode)
	}
	if got.Connection.FailureThreshold != 5 {
		t.Errorf("connection.failure_threshold 期望默认值 5，实际 %d", got.Connection.FailureThreshold)
	}
}

// TestLoadEnvConfigFailsWhenRequiredMissing 验证必需环境变量缺失（为空）时
// LoadEnvConfig 返回校验类错误（Req 18.3）。
func TestLoadEnvConfigFailsWhenRequiredMissing(t *testing.T) {
	setRequiredEnv(t)
	// 将必需的 PG DSN 置空，触发 notEmpty 校验失败。
	t.Setenv("MPG_PG_DSN", "")

	_, err := LoadEnvConfig()
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
	}
}

// TestLoadFailsWhenRequiredEnvMissing 验证必需环境变量缺失时 Load 直接返回错误并终止，
// 不会创建任何配置文件（Req 18.3）。注：MPG_ENCRYPTION_KEY 已改为可选（缺失时回退到内置默认密钥），
// 故此处用 MPG_PG_DSN 验证「真正必需」的环境变量缺失即触发早失败。
func TestLoadFailsWhenRequiredEnvMissing(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("MPG_PG_DSN", "")
	dataDir := t.TempDir()

	_, err := Load(nil, dataDir)
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
	}

	// 环境变量校验失败应早于 YAML 处理，故不应创建配置文件。
	if _, statErr := os.Stat(filepath.Join(dataDir, "config.yaml")); !os.IsNotExist(statErr) {
		t.Errorf("环境变量校验失败时不应创建配置文件，Stat 结果：%v", statErr)
	}
}

// TestLoadFailsOnInvalidYAML 验证 config.yaml 内容非法时 Load 返回解析错误并终止（Req 18.6）。
func TestLoadFailsOnInvalidYAML(t *testing.T) {
	setRequiredEnv(t)
	dataDir := t.TempDir()
	yamlPath := filepath.Join(dataDir, "config.yaml")

	// "a: b: c" 触发 yaml.v3 的「mapping values are not allowed」解析错误。
	if err := os.WriteFile(yamlPath, []byte("a: b: c\n"), 0o644); err != nil {
		t.Fatalf("写入非法 YAML 失败：%v", err)
	}

	_, err := Load(nil, dataDir)
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
	}
}

// TestSavePersistsConfig 验证 Save 校验通过后写盘，重新加载得到一致配置（Req 18.4）。
func TestSavePersistsConfig(t *testing.T) {
	setRequiredEnv(t)
	dataDir := t.TempDir()

	mgr, err := Load(nil, dataDir)
	if err != nil {
		t.Fatalf("首次 Load 失败：%v", err)
	}

	// 修改若干合法字段后回写。
	updated := mgr.Config()
	updated.Auth.SessionTimeoutS = 7200
	updated.MCPAPI.Mode = ModeFull
	updated.Statistics.RetentionDays = 30
	if err := mgr.Save(updated); err != nil {
		t.Fatalf("Save 不应返回错误：%v", err)
	}

	// 内存快照应立即更新。
	if got := mgr.Config(); got != updated {
		t.Errorf("Save 后内存快照不一致：\n期望 %+v\n实际 %+v", updated, got)
	}

	// 重新加载（新 Manager）应读取到回写后的配置。
	reloaded, err := Load(nil, dataDir)
	if err != nil {
		t.Fatalf("再次 Load 失败：%v", err)
	}
	if got := reloaded.Config(); got != updated {
		t.Errorf("回写持久化后重新读取不一致：\n期望 %+v\n实际 %+v", updated, got)
	}
}

// TestSaveRejectsInvalidConfig 验证 Save 在配置非法时不写盘、不更新内存快照并返回校验错误。
func TestSaveRejectsInvalidConfig(t *testing.T) {
	setRequiredEnv(t)
	dataDir := t.TempDir()

	mgr, err := Load(nil, dataDir)
	if err != nil {
		t.Fatalf("Load 失败：%v", err)
	}
	original := mgr.Config()

	// 构造越界配置：会话超时低于下限 300。
	invalid := original
	invalid.Auth.SessionTimeoutS = 1

	err = mgr.Save(invalid)
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
	}

	// 内存快照应保持不变。
	if got := mgr.Config(); got != original {
		t.Errorf("非法 Save 不应更改内存快照：\n期望 %+v\n实际 %+v", original, got)
	}
}

// TestLoadEnvConfigSuccess 验证全部必需环境变量齐备时 LoadEnvConfig 正确解析，
// 且 DataDir 在未设置时采用默认值 /data（Req 18.1）。
func TestLoadEnvConfigSuccess(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := LoadEnvConfig()
	if err != nil {
		t.Fatalf("LoadEnvConfig 不应返回错误：%v", err)
	}
	if cfg.PGDSN == "" || cfg.RedisAddr == "" || cfg.EncryptionKey == "" {
		t.Errorf("必需字段不应为空：%+v", cfg)
	}
	if cfg.DataDir != "/data" {
		t.Errorf("DataDir 期望默认值 /data，实际 %q", cfg.DataDir)
	}
}
