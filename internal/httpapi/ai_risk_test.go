package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/risk"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

type fakeAIRiskService struct {
	providers  []risk.Provider
	testResult risk.ProviderTestResult
}

func (f *fakeAIRiskService) ListProviders(context.Context) ([]risk.Provider, error) {
	return f.providers, nil
}

func (f *fakeAIRiskService) GetProvider(context.Context, string) (risk.Provider, error) {
	if len(f.providers) == 0 {
		return risk.Provider{}, nil
	}
	return f.providers[0], nil
}

func (f *fakeAIRiskService) CreateProvider(_ context.Context, in risk.ProviderInput) (risk.Provider, error) {
	return risk.Provider{ID: "new", Name: in.Name}, nil
}

func (f *fakeAIRiskService) UpdateProvider(_ context.Context, id string, in risk.ProviderInput) (risk.Provider, error) {
	return risk.Provider{ID: id, Name: in.Name}, nil
}

func (f *fakeAIRiskService) DeleteProvider(context.Context, string) error { return nil }

func (f *fakeAIRiskService) ActivateProvider(context.Context, string) error { return nil }

func (f *fakeAIRiskService) TestProvider(context.Context, string) (risk.ProviderTestResult, error) {
	return f.testResult, nil
}

func (f *fakeAIRiskService) QueueAssessment(context.Context, int) (risk.AssessmentJob, error) {
	return risk.AssessmentJob{}, nil
}

func (f *fakeAIRiskService) QueueReviewAssessment(context.Context, int) (risk.AssessmentJob, error) {
	return risk.AssessmentJob{}, nil
}

func (f *fakeAIRiskService) ListJobs(context.Context, int) ([]risk.AssessmentJob, error) {
	return nil, nil
}

func (f *fakeAIRiskService) GetJob(context.Context, string) (risk.AssessmentJob, error) {
	return risk.AssessmentJob{}, nil
}

func (f *fakeAIRiskService) CancelJob(context.Context, string) error { return nil }

func (f *fakeAIRiskService) ReassessTool(context.Context, string, string) (risk.Assessment, error) {
	return risk.Assessment{}, nil
}

type fakeToolRiskStore struct{}

func (fakeToolRiskStore) Get(context.Context, string, string) (risk.Assessment, error) {
	return risk.Assessment{}, nil
}

func (fakeToolRiskStore) List(context.Context, store.RiskListQuery) (store.RiskListResult, error) {
	return store.RiskListResult{}, nil
}

func (fakeToolRiskStore) Reconcile(context.Context, string, []domain.ToolDef) (store.ReconcileResult, error) {
	return store.ReconcileResult{}, nil
}

func (fakeToolRiskStore) SetManualOverride(context.Context, string, string, risk.Level, []string, string, bool) (risk.Assessment, error) {
	return risk.Assessment{}, nil
}

func (fakeToolRiskStore) BulkSetManualOverride(context.Context, []store.RiskOverrideTarget, risk.Level, []string, string, bool) ([]risk.Assessment, error) {
	return nil, nil
}

func (fakeToolRiskStore) ClearManualOverride(context.Context, string, string) (risk.Assessment, error) {
	return risk.Assessment{}, nil
}

func TestListAIProvidersDoesNotExposeAPIKey(t *testing.T) {
	service := &fakeAIRiskService{providers: []risk.Provider{{
		ID: "provider-1", Name: "OpenAI", BaseURL: "https://example.com/v1",
		APIStyle: risk.APIStyleChatCompletions, Model: "gpt-test", APIKey: "sk-list-secret",
		Enabled: true, TimeoutS: 60, BatchSize: 10, MaxConcurrency: 1,
	}}}
	engine := newTestEngine(Deps{AIRisk: service, ToolRiskStore: fakeToolRiskStore{}})

	response := doJSON(engine, http.MethodGet, "/api/admin/ai-risk/providers", "")
	if response.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d：%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "sk-list-secret") {
		t.Fatalf("Provider 列表不应暴露 API Key：%s", response.Body.String())
	}

	var data struct {
		Providers []map[string]json.RawMessage `json:"providers"`
	}
	unmarshalData(t, response, &data)
	if len(data.Providers) != 1 {
		t.Fatalf("Provider 数量 = %d，期望 1", len(data.Providers))
	}
	for _, field := range []string{"apiKey", "batchSize", "maxConcurrency", "autoAssess", "createdAt", "updatedAt"} {
		if _, exists := data.Providers[0][field]; exists {
			t.Fatalf("Provider 列表响应不应包含 %s 字段：%s", field, response.Body.String())
		}
	}
	if _, exists := data.Providers[0]["model"]; !exists {
		t.Fatalf("Provider 列表应保留基本模型信息：%s", response.Body.String())
	}
}

func TestGetAIProviderReturnsAPIKeyForEditor(t *testing.T) {
	service := &fakeAIRiskService{providers: []risk.Provider{{
		ID: "provider-1", Name: "OpenAI", BaseURL: "https://example.com/v1",
		APIStyle: risk.APIStyleChatCompletions, Model: "gpt-test", APIKey: "sk-editor-secret",
		Enabled: true, TimeoutS: 60, BatchSize: 10, MaxConcurrency: 1,
	}}}
	engine := newTestEngine(Deps{AIRisk: service, ToolRiskStore: fakeToolRiskStore{}})

	response := doJSON(engine, http.MethodGet, "/api/admin/ai-risk/providers/provider-1", "")
	if response.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d：%s", response.Code, response.Body.String())
	}
	var provider risk.Provider
	unmarshalData(t, response, &provider)
	if provider.APIKey != "sk-editor-secret" {
		t.Fatalf("编辑详情未返回 API Key：%+v", provider)
	}
}

func TestAIProviderTestReturnsLatencyAndTokenUsage(t *testing.T) {
	input, output, total := int64(120), int64(34), int64(154)
	service := &fakeAIRiskService{testResult: risk.ProviderTestResult{
		LatencyMS: 567, InputTokens: &input, OutputTokens: &output, TotalTokens: &total,
	}}
	engine := newTestEngine(Deps{AIRisk: service, ToolRiskStore: fakeToolRiskStore{}})

	response := doJSON(engine, http.MethodPost, "/api/admin/ai-risk/providers/provider-1/test", "")
	if response.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d：%s", response.Code, response.Body.String())
	}
	var data providerTestResponse
	unmarshalData(t, response, &data)
	if !data.OK || data.LatencyMS != 567 {
		t.Fatalf("测试响应状态或延迟不正确：%+v", data)
	}
	if data.InputTokens == nil || *data.InputTokens != input ||
		data.OutputTokens == nil || *data.OutputTokens != output ||
		data.TotalTokens == nil || *data.TotalTokens != total {
		t.Fatalf("测试响应 Token 用量不正确：%+v", data)
	}
}
