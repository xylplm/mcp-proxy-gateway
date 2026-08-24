package risk

import (
	"context"
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
