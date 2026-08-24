package risk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type batchTestCatalog struct {
	applied atomic.Int32
	item    Assessment
}

func (c *batchTestCatalog) Get(context.Context, string, string) (Assessment, error) {
	return c.item, nil
}

func (c *batchTestCatalog) ListAssessable(context.Context, int) ([]Assessment, error) {
	return nil, nil
}

func (c *batchTestCatalog) ListNeedsReview(context.Context, int) ([]Assessment, error) {
	return nil, nil
}

func (c *batchTestCatalog) ApplyAIResult(context.Context, string, string, AIResult, Provider) (Assessment, error) {
	c.applied.Add(1)
	return Assessment{Status: StatusRated}, nil
}

func (c *batchTestCatalog) MarkAIError(context.Context, string, string, string) error {
	return nil
}

type observerTestProviders struct {
	provider Provider
}

func (p *observerTestProviders) Create(_ context.Context, provider Provider) (Provider, error) {
	p.provider = provider
	return provider, nil
}

func (p *observerTestProviders) Update(_ context.Context, provider Provider) (Provider, error) {
	p.provider = provider
	return provider, nil
}

func (p *observerTestProviders) Get(context.Context, string) (Provider, error) {
	return p.provider, nil
}

func (p *observerTestProviders) Active(context.Context) (Provider, error) {
	return p.provider, nil
}

func (p *observerTestProviders) List(context.Context) ([]Provider, error) {
	return []Provider{p.provider}, nil
}

func (p *observerTestProviders) Activate(context.Context, string) error { return nil }

func (p *observerTestProviders) Delete(context.Context, string) error { return nil }

func TestProcessBatchSplitsRetryableProviderFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) <= 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("解析请求失败: %v", err)
		}
		var prompt struct {
			Tools []AssessmentInput `json:"tools"`
		}
		if err := json.Unmarshal([]byte(body.Messages[1].Content), &prompt); err != nil {
			t.Fatalf("解析提示词失败: %v", err)
		}
		results := make([]AIResult, 0, len(prompt.Tools))
		for _, tool := range prompt.Tools {
			results = append(results, AIResult{ToolID: tool.ToolID, FunctionSummaryZh: "读取工具信息", RiskLevel: LevelLow, Confidence: 0.9, Reason: "只读", ReviewReason: "none"})
		}
		content, _ := json.Marshal(map[string]any{"assessments": results})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": string(content)}}},
		})
	}))
	defer server.Close()

	catalog := &batchTestCatalog{}
	service := &GovernanceService{catalog: catalog, client: NewOpenAIClient()}
	provider := Provider{Name: "mock", BaseURL: server.URL, APIStyle: APIStyleChatCompletions, Model: "mock",
		Enabled: true, TimeoutS: 2, BatchSize: 2, MaxConcurrency: 1}
	batch := []Assessment{
		{UpstreamID: "up", OriginalName: "one"},
		{UpstreamID: "up", OriginalName: "two"},
	}

	outcome := service.processBatch(context.Background(), provider, "", batch, true)
	if outcome.success != 2 || outcome.failure != 0 || outcome.processed != 2 {
		t.Fatalf("结果统计不符: %+v", outcome)
	}
	if outcome.retries != 2 || outcome.splits != 1 {
		t.Fatalf("重试/拆分统计不符: %+v", outcome)
	}
	if catalog.applied.Load() != 2 || requests.Load() != 5 {
		t.Fatalf("请求或写入次数不符: requests=%d applied=%d", requests.Load(), catalog.applied.Load())
	}
}

func TestReassessToolNotifiesCatalogObserverAfterSuccessfulPersist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		content, err := json.Marshal(map[string]any{"assessments": []AIResult{{
			ToolID:            "up:read",
			FunctionSummaryZh: "读取工具信息",
			RiskLevel:         LevelLow,
			RiskTags:          []string{"read"},
			Confidence:        0.9,
			Reason:            "只读",
			ReviewReason:      "none",
		}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": string(content)}}},
		}); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	catalog := &batchTestCatalog{item: Assessment{
		UpstreamID: "up", OriginalName: "read", ExposedName: "read", Description: "读取数据",
	}}
	providers := &observerTestProviders{provider: Provider{
		ID: "provider-1", Name: "mock", BaseURL: server.URL, APIStyle: APIStyleChatCompletions,
		Model: "mock", Enabled: true, TimeoutS: 2, BatchSize: 1, MaxConcurrency: 1,
	}}
	var notifications atomic.Int32
	service := NewGovernanceService(
		providers,
		catalog,
		nil,
		nil,
		WithCatalogChangeObserver(func() { notifications.Add(1) }),
	)

	if _, err := service.ReassessTool(context.Background(), "up", "read"); err != nil {
		t.Fatal(err)
	}
	if catalog.applied.Load() != 1 || notifications.Load() != 1 {
		t.Fatalf("successful reassessment must persist then notify exactly once: applied=%d notifications=%d", catalog.applied.Load(), notifications.Load())
	}
}
