package risk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const maxProviderResponseBytes = 2 << 20

type OpenAIClient struct{}

type ProviderRequestError struct {
	StatusCode int
	Retryable  bool
	RetryAfter time.Duration
}

func (e *ProviderRequestError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("AI Provider 返回 HTTP %d", e.StatusCode)
	}
	return "AI Provider 请求失败"
}

func NewOpenAIClient() *OpenAIClient { return &OpenAIClient{} }

func (c *OpenAIClient) Assess(ctx context.Context, provider Provider, apiKey string, inputs []AssessmentInput) ([]AIResult, error) {
	if err := ValidateProvider(provider); err != nil {
		return nil, err
	}
	prompt, err := BuildPrompt(inputs)
	if err != nil {
		return nil, err
	}
	expected := make([]string, 0, len(inputs))
	for _, input := range inputs {
		expected = append(expected, input.ToolID)
	}

	endpoint, body, err := providerRequest(provider, prompt)
	if err != nil {
		return nil, err
	}
	raw, err := c.do(ctx, provider, apiKey, endpoint, body)
	if err != nil {
		return nil, err
	}
	content, err := extractProviderContent(provider.APIStyle, raw)
	if err != nil {
		return nil, err
	}
	return ParseAssessmentResponse(content, expected)
}

func (c *OpenAIClient) TestConnection(ctx context.Context, provider Provider, apiKey string) (time.Duration, error) {
	started := time.Now()
	_, err := c.Assess(ctx, provider, apiKey, []AssessmentInput{{ToolID: "connection-test", OriginalName: "health_check", Description: "只读连接测试"}})
	return time.Since(started), err
}

func (c *OpenAIClient) do(ctx context.Context, provider Provider, apiKey, endpoint string, body []byte) ([]byte, error) {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	client := &http.Client{Transport: transport, Timeout: time.Duration(provider.TimeoutS) * time.Second}
	base, _ := url.Parse(provider.BaseURL)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !strings.EqualFold(req.URL.Hostname(), base.Hostname()) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("创建 AI 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &ProviderRequestError{Retryable: true}
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxProviderResponseBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("读取 AI Provider 响应失败")
	}
	if len(raw) > maxProviderResponseBytes {
		return nil, fmt.Errorf("AI Provider 响应超过 2 MiB 限制")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &ProviderRequestError{StatusCode: resp.StatusCode, Retryable: resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	return raw, nil
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(0, time.Until(at))
	}
	return 0
}

func providerRequest(provider Provider, prompt Prompt) (string, []byte, error) {
	base, err := url.Parse(provider.BaseURL)
	if err != nil {
		return "", nil, err
	}
	if provider.APIStyle == APIStyleResponses {
		base.Path = path.Join(base.Path, "responses")
		body, err := json.Marshal(map[string]any{"model": provider.Model, "instructions": prompt.System, "input": prompt.UserJSON})
		return base.String(), body, err
	}
	base.Path = path.Join(base.Path, "chat/completions")
	body, err := json.Marshal(map[string]any{
		"model": provider.Model, "temperature": 0,
		"messages":        []map[string]string{{"role": "system", "content": prompt.System}, {"role": "user", "content": prompt.UserJSON}},
		"response_format": assessmentResponseFormat(),
	})
	return base.String(), body, err
}

func assessmentResponseFormat() map[string]any {
	return map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name": "mcp_tool_risk_assessments", "strict": true,
			"schema": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"assessments"},
				"properties": map[string]any{"assessments": map[string]any{
					"type": "array", "items": map[string]any{
						"type": "object", "additionalProperties": false,
						"required": []string{"toolId", "functionSummaryZh", "riskLevel", "riskTags", "confidence", "reason", "requiresReview", "reviewReason"},
						"properties": map[string]any{
							"toolId": map[string]any{"type": "string"}, "riskLevel": map[string]any{"type": "string", "enum": []string{"low", "medium", "high", "blocked"}},
							"functionSummaryZh": map[string]any{"type": "string", "maxLength": 500},
							"riskTags":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
							"reason": map[string]any{"type": "string", "maxLength": 500}, "requiresReview": map[string]any{"type": "boolean"},
							"reviewReason": map[string]any{"type": "string", "enum": []string{"none", "insufficient_evidence", "ambiguous_scope", "conflicting_signals"}},
						},
					},
				}},
			},
		},
	}
}

func extractProviderContent(style APIStyle, raw []byte) ([]byte, error) {
	if style == APIStyleResponses {
		var body struct {
			OutputText string `json:"output_text"`
			Output     []struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"output"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, fmt.Errorf("解析 Responses 响应失败")
		}
		if body.OutputText != "" {
			return []byte(body.OutputText), nil
		}
		for _, out := range body.Output {
			for _, content := range out.Content {
				if content.Text != "" {
					return []byte(content.Text), nil
				}
			}
		}
		return nil, fmt.Errorf("Responses 响应缺少文本结果")
	}
	var body struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("解析 Chat Completions 响应失败")
	}
	if len(body.Choices) == 0 || body.Choices[0].Message.Content == "" {
		return nil, fmt.Errorf("Chat Completions 响应缺少结果")
	}
	return []byte(body.Choices[0].Message.Content), nil
}
