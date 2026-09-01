package risk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAIClientSanitizesAndClassifiesHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		status     int
		retryable  bool
		retryAfter time.Duration
	}{{http.StatusBadRequest, false, 0}, {http.StatusTooManyRequests, true, 7 * time.Second}, {http.StatusBadGateway, true, 0}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer top-secret" {
				t.Errorf("Authorization 未正确发送")
			}
			if tc.retryAfter > 0 {
				w.Header().Set("Retry-After", "7")
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"error":"top-secret"}`))
		}))
		provider := Provider{Name: "mock", BaseURL: server.URL, APIStyle: APIStyleChatCompletions, Model: "mock", Enabled: true, TimeoutS: 2, BatchSize: 1, MaxConcurrency: 1}
		_, err := NewOpenAIClient().Assess(context.Background(), provider, "top-secret", []AssessmentInput{{ToolID: "up:a", OriginalName: "a"}})
		server.Close()
		var requestErr *ProviderRequestError
		if !errors.As(err, &requestErr) || requestErr.Retryable != tc.retryable {
			t.Fatalf("status=%d err=%v", tc.status, err)
		}
		if requestErr.RetryAfter != tc.retryAfter {
			t.Fatalf("status=%d Retry-After=%v，期望 %v", tc.status, requestErr.RetryAfter, tc.retryAfter)
		}
		if strings.Contains(err.Error(), "top-secret") {
			t.Fatal("错误信息泄露了 Provider API Key")
		}
	}
}

func TestOpenAIClientTestConnectionReturnsTokenUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("请求路径 = %q，期望 /chat/completions", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization 未正确发送")
		}
		content := `{"assessments":[{"toolId":"connection-test","functionSummaryZh":"连接测试","riskLevel":"low","riskTags":[],"confidence":1,"reason":"只读测试请求","requiresReview":false,"reviewReason":"none"}]}`
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": content}}},
			"usage":   map[string]int64{"prompt_tokens": 123, "completion_tokens": 45, "total_tokens": 168},
		})
	}))
	defer server.Close()

	provider := Provider{
		Name: "mock", BaseURL: server.URL, APIStyle: APIStyleChatCompletions, Model: "mock",
		Enabled: true, TimeoutS: 2, BatchSize: 1, MaxConcurrency: 1,
	}
	result, err := NewOpenAIClient().TestConnection(context.Background(), provider, "test-token")
	if err != nil {
		t.Fatalf("测试连接失败：%v", err)
	}
	if result.LatencyMS < 0 {
		t.Fatalf("延迟不能为负数：%d", result.LatencyMS)
	}
	if result.InputTokens == nil || *result.InputTokens != 123 {
		t.Fatalf("输入 Token = %v，期望 123", result.InputTokens)
	}
	if result.OutputTokens == nil || *result.OutputTokens != 45 {
		t.Fatalf("输出 Token = %v，期望 45", result.OutputTokens)
	}
	if result.TotalTokens == nil || *result.TotalTokens != 168 {
		t.Fatalf("总 Token = %v，期望 168", result.TotalTokens)
	}
}

func TestOpenAIClientTestConnectionReportsProviderTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// Client.Timeout cancels the client-side request, but an HTTP/1.1 server
		// is not guaranteed to observe that disconnect immediately on Windows.
		// The upper bound keeps cleanup deterministic without affecting the
		// client's one-second timeout assertion below.
		select {
		case <-r.Context().Done():
		case <-time.After(1500 * time.Millisecond):
		}
	}))
	defer server.Close()

	provider := Provider{
		Name: "mock", BaseURL: server.URL, APIStyle: APIStyleChatCompletions, Model: "mock",
		Enabled: true, TimeoutS: 1, BatchSize: 1, MaxConcurrency: 1,
	}
	_, err := NewOpenAIClient().TestConnection(context.Background(), provider, "test-token")
	var requestErr *ProviderRequestError
	if !errors.As(err, &requestErr) || !requestErr.TimedOut || !requestErr.Retryable {
		t.Fatalf("超时应归类为可重试的 ProviderRequestError，实际 %v", err)
	}
	if err.Error() != "AI Provider 请求超时" {
		t.Fatalf("超时提示 = %q", err.Error())
	}
}

func TestExtractProviderTokenUsageSupportsBothOpenAIShapes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		raw    string
		input  int64
		output int64
		total  int64
	}{
		{
			name:  "chat completions",
			raw:   `{"usage":{"prompt_tokens":17,"completion_tokens":9,"total_tokens":26}}`,
			input: 17, output: 9, total: 26,
		},
		{
			name:  "responses without total",
			raw:   `{"usage":{"input_tokens":20,"output_tokens":6}}`,
			input: 20, output: 6, total: 26,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := extractProviderTokenUsage([]byte(tc.raw))
			if result.InputTokens == nil || *result.InputTokens != tc.input {
				t.Fatalf("输入 Token = %v，期望 %d", result.InputTokens, tc.input)
			}
			if result.OutputTokens == nil || *result.OutputTokens != tc.output {
				t.Fatalf("输出 Token = %v，期望 %d", result.OutputTokens, tc.output)
			}
			if result.TotalTokens == nil || *result.TotalTokens != tc.total {
				t.Fatalf("总 Token = %v，期望 %d", result.TotalTokens, tc.total)
			}
		})
	}
}
