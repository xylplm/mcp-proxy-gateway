package aggregation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

type invConcurrentRecordingInvoker struct {
	calls atomic.Int64
}

func (i *invConcurrentRecordingInvoker) CallUpstream(_ context.Context, _ string, _ string, args json.RawMessage) (domain.ToolResult, error) {
	i.calls.Add(1)
	content := append(json.RawMessage(nil), args...)
	return domain.ToolResult{Content: content}, nil
}

func TestInvokeToolConcurrentAPIKeyVisibilityIsolated(t *testing.T) {
	cache := &invFakeCache{tools: map[string][]domain.ToolDef{
		"up-a": {{OriginalName: "read_file", Name: "read_file"}},
	}}
	upstreams := &invFakeUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up-a", 0)}}
	aliases := &invFakeAliases{byUpstream: map[string][]domain.AliasRule{}}
	mcpFilters := &invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}}
	apiKeyFilters := &invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{
		"blocked-key": {{Pattern: "read_file", IsRegex: false, Enabled: true}},
	}}
	invoker := &invConcurrentRecordingInvoker{}
	svc := invNewService(cache, upstreams, aliases, mcpFilters, apiKeyFilters).SetInvoker(invoker)

	const workers = 128
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			apiKeyID := "open-key"
			wantBlocked := false
			if i%2 == 0 {
				apiKeyID = "blocked-key"
				wantBlocked = true
			}
			args := json.RawMessage(fmt.Sprintf(`{"seq":%d}`, i))
			got, err := svc.InvokeTool(context.Background(), apiKeyID, "read_file", args)
			if wantBlocked {
				if !isToolNotFound(err) {
					errCh <- fmt.Errorf("blocked call %d should be rejected before upstream forwarding, got %v", i, err)
					return
				}
				errCh <- nil
				return
			}
			if err != nil {
				errCh <- fmt.Errorf("open call %d should succeed: %w", i, err)
				return
			}
			if string(got.Content) != string(args) {
				errCh <- fmt.Errorf("open call %d response crossed data boundary: got %s want %s", i, got.Content, args)
				return
			}
			errCh <- nil
		}()
	}

	for i := 0; i < workers; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
	if got, want := invoker.calls.Load(), int64(workers/2); got != want {
		t.Fatalf("only visible API key calls should be forwarded, got %d want %d", got, want)
	}
}

func isToolNotFound(err error) bool {
	var apiErr *domain.APIError
	return errors.As(err, &apiErr) && apiErr.Code == domain.CodeToolNotFound
}
