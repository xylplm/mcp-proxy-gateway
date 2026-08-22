package aggregation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

type denyAtDispatchAuthorizer struct{}

func (denyAtDispatchAuthorizer) FilterSources(_ context.Context, _, _ string, tools []domain.ToolDef) ([]domain.ToolDef, error) {
	return tools, nil
}
func (denyAtDispatchAuthorizer) AuthorizeSource(_ context.Context, _, _, _ string) error {
	return domain.NewError(domain.CodeToolRiskForbidden, "forbidden")
}

func TestInvokeToolRiskRecheckedBeforeUpstreamCall(t *testing.T) {
	cache := &invFakeCache{tools: map[string][]domain.ToolDef{"up-a": {{OriginalName: "write", Name: "write"}}}}
	svc := invNewService(cache,
		&invFakeUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up-a", 0)}},
		&invFakeAliases{byUpstream: map[string][]domain.AliasRule{}},
		&invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}},
		&invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{}},
	)
	invoker := &invRecordingInvoker{}
	svc.SetInvoker(invoker).SetRiskAuthorizer(denyAtDispatchAuthorizer{})
	_, err := svc.InvokeTool(context.Background(), "key-1", "write", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("调用前风险复核拒绝时应返回错误")
	}
	apiErr, ok := err.(*domain.APIError)
	if !ok || apiErr.Code != domain.CodeToolRiskForbidden {
		t.Fatalf("错误码 = %v", err)
	}
	if invoker.called {
		t.Fatal("风险拒绝后不得调用上游")
	}
}
