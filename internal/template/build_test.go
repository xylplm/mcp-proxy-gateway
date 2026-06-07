package template

import (
	"errors"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件为任务 20.3「基于模板的上游创建」的单元测试，覆盖 Req 14.7-14.12：
//   - Prefill 返回预填充表单与占位参数定义（Req 14.7）；
//   - 缺失必填占位参数被拒绝并指明参数名（Req 14.10）；
//   - 服务地址非法被拒绝且保留其他已填参数（Req 14.9）；
//   - 占位取值正确注入预设参数的 ${name} 引用，secret 占位作为凭证（Req 14.12）；
//   - 生成配置按需求 2/4 字段校验（Req 14.11）。
//
// 辅助/标识符以 bu 前缀命名，避免与同包其它测试（market_test）冲突。

// buAsAPIError 将 error 断言为 *domain.APIError。
func buAsAPIError(t *testing.T, err error) *domain.APIError {
	t.Helper()
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望 *domain.APIError，got %T: %v", err, err)
	}
	return apiErr
}

// TestPrefillReturnsFormAndPlaceholders 验证选择模板返回预填充表单与占位参数（Req 14.7）。
func TestPrefillReturnsFormAndPlaceholders(t *testing.T) {
	m := New()
	form, err := m.Prefill("modelscope-mcp")
	if err != nil {
		t.Fatalf("Prefill 不应出错：%v", err)
	}
	if form.TemplateID != "modelscope-mcp" || form.Name == "" {
		t.Fatalf("预填充表单字段缺失：%+v", form)
	}
	if form.Transport != domain.TransportStreamableHTTP {
		t.Fatalf("传输类型应来自模板，got=%q", form.Transport)
	}
	if len(form.Placeholders) != 2 {
		t.Fatalf("应返回 2 个占位参数，got=%d", len(form.Placeholders))
	}
	// 返回的是深拷贝，修改不应影响市场内部数据。
	form.PresetParams["injected"] = "x"
	again, _ := m.Prefill("modelscope-mcp")
	if _, ok := again.PresetParams["injected"]; ok {
		t.Fatalf("Prefill 应返回深拷贝，内部数据被污染")
	}
}

// TestPrefillNotFound 验证模板不存在时返回 NOT_FOUND。
func TestPrefillNotFound(t *testing.T) {
	m := New()
	_, err := m.Prefill("ghost")
	if buAsAPIError(t, err).Code != domain.CodeNotFound {
		t.Fatalf("应返回 NOT_FOUND")
	}
}

// TestBuildUpstreamMissingRequiredParam 验证缺失必填占位参数被拒绝并指明参数名（Req 14.10）。
func TestBuildUpstreamMissingRequiredParam(t *testing.T) {
	m := New()
	// modelscope 需 url 与 apiKey；仅提供 apiKey，缺失 url。
	_, err := m.BuildUpstream("modelscope-mcp", BuildInput{
		Values: map[string]string{"apiKey": "sk-123"},
	})
	apiErr := buAsAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Fatalf("应返回 VALIDATION，got=%s", apiErr.Code)
	}
	if _, ok := apiErr.Fields["url"]; !ok {
		t.Fatalf("应指明缺失的必填参数 url，fields=%v", apiErr.Fields)
	}
}

// TestBuildUpstreamInvalidURLKeepsOtherParams 验证服务地址非法被拒绝（Req 14.9）。
func TestBuildUpstreamInvalidURLKeepsOtherParams(t *testing.T) {
	m := New()
	_, err := m.BuildUpstream("modelscope-mcp", BuildInput{
		Values: map[string]string{"url": "not-a-url", "apiKey": "sk-123"},
	})
	apiErr := buAsAPIError(t, err)
	if _, ok := apiErr.Fields["url"]; !ok {
		t.Fatalf("应指明 url 格式非法，fields=%v", apiErr.Fields)
	}
	// apiKey 合法，不应出现在错误字段中（保留其他已填参数）。
	if _, ok := apiErr.Fields["apiKey"]; ok {
		t.Fatalf("合法的 apiKey 不应报错，fields=%v", apiErr.Fields)
	}
}

// TestBuildUpstreamSuccessInjectsAndSetsCredential 验证成功创建：注入 ${name} 并设置凭证（Req 14.12）。
func TestBuildUpstreamSuccessInjectsAndSetsCredential(t *testing.T) {
	m := New()
	cfg, err := m.BuildUpstream("modelscope-mcp", BuildInput{
		Name:    "我的魔搭",
		Values:  map[string]string{"url": "https://api.modelscope.cn/mcp", "apiKey": "sk-secret"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("合法参数不应出错：%v", err)
	}
	if cfg.Name != "我的魔搭" || cfg.Transport != domain.TransportStreamableHTTP {
		t.Fatalf("上游基础字段错误：%+v", cfg)
	}
	if !cfg.Enabled {
		t.Fatalf("Enabled 应透传为 true")
	}
	// secret 占位值应作为凭证。
	if cfg.Credential != "sk-secret" {
		t.Fatalf("secret 占位应作为凭证，got=%q", cfg.Credential)
	}
	// ${apiKey} 应被注入到 headers.Authorization。
	headers, ok := cfg.ConnParams["headers"].(map[string]any)
	if !ok {
		t.Fatalf("headers 应为 map，got=%T", cfg.ConnParams["headers"])
	}
	if headers["Authorization"] != "Bearer sk-secret" {
		t.Fatalf("${apiKey} 未正确注入，got=%v", headers["Authorization"])
	}
	// url 占位应注入到 url 连接参数。
	if cfg.ConnParams["url"] != "https://api.modelscope.cn/mcp" {
		t.Fatalf("url 未正确注入，got=%v", cfg.ConnParams["url"])
	}
}

// TestBuildUpstreamNameFallsBackToTemplate 验证名称为空时回退到模板名称。
func TestBuildUpstreamNameFallsBackToTemplate(t *testing.T) {
	m := New()
	cfg, err := m.BuildUpstream("tavily-search", BuildInput{
		Values: map[string]string{"apiKey": "tvly-123"},
	})
	if err != nil {
		t.Fatalf("不应出错：%v", err)
	}
	if cfg.Name != "Tavily 网页搜索" {
		t.Fatalf("名称应回退到模板名，got=%q", cfg.Name)
	}
}

// TestBuildUpstreamInjectsIntoArgsSlice 验证注入对 args 字符串数组生效（stdio 模板）。
func TestBuildUpstreamInjectsIntoArgsSlice(t *testing.T) {
	m := New()
	cfg, err := m.BuildUpstream("postgres-mcp", BuildInput{
		Values: map[string]string{"dsn": "postgresql://u:p@localhost:5432/db"},
	})
	if err != nil {
		t.Fatalf("不应出错：%v", err)
	}
	args, ok := cfg.ConnParams["args"].([]any)
	if !ok {
		t.Fatalf("args 应为 []any，got=%T", cfg.ConnParams["args"])
	}
	last := args[len(args)-1]
	if last != "postgresql://u:p@localhost:5432/db" {
		t.Fatalf("${dsn} 未注入到 args 末位，got=%v", last)
	}
}

// TestBuildUpstreamNoPlaceholderTemplate 验证无占位参数的模板可直接创建。
func TestBuildUpstreamNoPlaceholderTemplate(t *testing.T) {
	m := New()
	cfg, err := m.BuildUpstream("fetch-mcp", BuildInput{})
	if err != nil {
		t.Fatalf("无占位模板不应出错：%v", err)
	}
	if cfg.Transport != domain.TransportStdio || cfg.Credential != "" {
		t.Fatalf("无凭证 stdio 模板配置错误：%+v", cfg)
	}
}
