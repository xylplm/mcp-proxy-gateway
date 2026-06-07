package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/template"
)

// 本文件以内置模板市场（template.New）注入 TemplateService，验证模板市场端点的
// 路由装配、查询参数处理与前端 json 契约对齐，无需额外 fake。

// TestListTemplatesWrapsInEnvelope 验证列表端点以 templates 字段包裹返回全部模板。
func TestListTemplatesWrapsInEnvelope(t *testing.T) {
	e := newTestEngine(Deps{Templates: template.New()})

	w := doJSON(e, http.MethodGet, "/api/admin/templates", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	var env struct {
		Templates []template.Template `json:"templates"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if len(env.Templates) == 0 {
		t.Fatalf("期望返回内置模板列表，实际为空")
	}
}

// TestListTemplatesByCategory 验证按分类筛选只返回该分类模板。
func TestListTemplatesByCategory(t *testing.T) {
	e := newTestEngine(Deps{Templates: template.New()})

	w := doJSON(e, http.MethodGet, "/api/admin/templates?category=search", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
	var env struct {
		Templates []template.Template `json:"templates"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if len(env.Templates) == 0 {
		t.Fatalf("期望返回 search 分类模板，实际为空")
	}
	for _, tpl := range env.Templates {
		if tpl.Category != template.CategorySearch {
			t.Errorf("期望仅 search 分类，实际出现 %q", tpl.Category)
		}
	}
}

// TestListTemplatesByCategoryAndKeyword 验证分类与关键字同时生效（先检索后按分类筛选）。
func TestListTemplatesByCategoryAndKeyword(t *testing.T) {
	e := newTestEngine(Deps{Templates: template.New()})

	// 关键字 "Tavily" 命中搜索分类模板；叠加 category=search 应仍命中。
	w := doJSON(e, http.MethodGet, "/api/admin/templates?category=search&keyword=Tavily", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
	var env struct {
		Templates []template.Template `json:"templates"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	for _, tpl := range env.Templates {
		if tpl.Category != template.CategorySearch {
			t.Errorf("叠加过滤后期望仅 search 分类，实际 %q", tpl.Category)
		}
	}

	// 关键字命中但分类不匹配时应返回空列表（非 nil）而非错误。
	w2 := doJSON(e, http.MethodGet, "/api/admin/templates?category=database&keyword=Tavily", "")
	if w2.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w2.Code)
	}
	// 验证序列化为 [] 而非 null（前端契约 Req 14.5、14.13）。
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w2.Body.Bytes(), &raw); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if string(raw["templates"]) != "[]" {
		t.Errorf("无匹配时期望 templates 为 []，实际 %s", string(raw["templates"]))
	}
}

// TestListTemplateCategories 验证分类视图端点以 categories 字段包裹并含中文显示名。
func TestListTemplateCategories(t *testing.T) {
	e := newTestEngine(Deps{Templates: template.New()})

	w := doJSON(e, http.MethodGet, "/api/admin/templates/categories", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	var env struct {
		Categories []template.CategoryView `json:"categories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if len(env.Categories) != len(template.Categories()) {
		t.Fatalf("期望返回 %d 个分类视图，实际 %d", len(template.Categories()), len(env.Categories))
	}
	for _, cv := range env.Categories {
		if cv.DisplayName == "" {
			t.Errorf("分类 %q 缺少中文显示名", cv.Category)
		}
	}
}

// TestGetTemplateByID 验证模板详情端点返回与前端契约对齐的 json 字段。
func TestGetTemplateByID(t *testing.T) {
	e := newTestEngine(Deps{Templates: template.New()})

	w := doJSON(e, http.MethodGet, "/api/admin/templates/tavily-search", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	// 直接断言原始 json key，确认与前端 TS 类型逐字段对齐（camelCase）。
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	for _, key := range []string{"id", "name", "category", "summary", "docUrl", "transport", "presetParams", "placeholders"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("模板详情缺少前端契约字段 %q", key)
		}
	}
	var tpl template.Template
	if err := json.Unmarshal(w.Body.Bytes(), &tpl); err != nil {
		t.Fatalf("解析模板失败：%v", err)
	}
	if tpl.ID != "tavily-search" {
		t.Errorf("期望返回 tavily-search，实际 %q", tpl.ID)
	}
}

// TestGetTemplateNotFoundMapsTo404 验证未知模板映射为 HTTP 404。
func TestGetTemplateNotFoundMapsTo404(t *testing.T) {
	e := newTestEngine(Deps{Templates: template.New()})

	w := doJSON(e, http.MethodGet, "/api/admin/templates/does-not-exist", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("期望 HTTP 404，实际 %d", w.Code)
	}
}

// TestPrefillTemplate 验证预填充端点返回与前端契约对齐的 PrefillForm json。
func TestPrefillTemplate(t *testing.T) {
	e := newTestEngine(Deps{Templates: template.New()})

	w := doJSON(e, http.MethodGet, "/api/admin/templates/tavily-search/prefill", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	for _, key := range []string{"templateId", "name", "transport", "presetParams", "placeholders"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("预填充表单缺少前端契约字段 %q", key)
		}
	}
	var form template.PrefillForm
	if err := json.Unmarshal(w.Body.Bytes(), &form); err != nil {
		t.Fatalf("解析预填充表单失败：%v", err)
	}
	if form.TemplateID != "tavily-search" {
		t.Errorf("期望预填充来源为 tavily-search，实际 %q", form.TemplateID)
	}
}

// TestPrefillTemplateNotFoundMapsTo404 验证未知模板预填充映射为 HTTP 404。
func TestPrefillTemplateNotFoundMapsTo404(t *testing.T) {
	e := newTestEngine(Deps{Templates: template.New()})

	w := doJSON(e, http.MethodGet, "/api/admin/templates/does-not-exist/prefill", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("期望 HTTP 404，实际 %d", w.Code)
	}
}

// TestTemplatesServiceUnavailableWhenNil 验证依赖未接线时返回 503。
func TestTemplatesServiceUnavailableWhenNil(t *testing.T) {
	e := newTestEngine(Deps{})

	w := doJSON(e, http.MethodGet, "/api/admin/templates", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("依赖未接线期望 HTTP 503，实际 %d", w.Code)
	}
}
