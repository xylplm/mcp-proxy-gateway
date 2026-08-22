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
}

func (c *batchTestCatalog) Get(context.Context, string, string) (Assessment, error) {
	return Assessment{}, nil
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
