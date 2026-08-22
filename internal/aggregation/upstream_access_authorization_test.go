package aggregation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

type selectedUpstreamAuthorizer struct {
	allowed      string
	denyDispatch bool
}

func (a selectedUpstreamAuthorizer) FilterUpstreams(_ context.Context, _ string, upstreams []domain.Upstream) ([]domain.Upstream, error) {
	out := make([]domain.Upstream, 0, len(upstreams))
	for _, upstream := range upstreams {
		if upstream.ID == a.allowed {
			out = append(out, upstream)
		}
	}
	return out, nil
}

func (a selectedUpstreamAuthorizer) AuthorizeUpstream(_ context.Context, _, upstreamID string) error {
	if a.denyDispatch || upstreamID != a.allowed {
		return domain.NewError(domain.CodeForbidden, "forbidden")
	}
	return nil
}

func TestBuildToolSetFiltersUnselectedUpstreamBeforeGrouping(t *testing.T) {
	cache := &invFakeCache{tools: map[string][]domain.ToolDef{
		"up-a": {{OriginalName: "same", Name: "same"}},
		"up-b": {{OriginalName: "same", Name: "same"}},
	}}
	svc := invNewService(cache,
		&invFakeUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up-a", 0), invEnabledUpstream("up-b", 1)}},
		&invFakeAliases{byUpstream: map[string][]domain.AliasRule{}},
		&invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}},
		&invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{}},
	)
	svc.SetUpstreamAccessAuthorizer(selectedUpstreamAuthorizer{allowed: "up-b"})
	_, reverse, err := svc.buildToolSetWithReverseMap(context.Background(), "key-1")
	if err != nil {
		t.Fatal(err)
	}
	entry := reverse["same"]
	if len(entry.Candidates) != 1 || entry.Candidates[0].UpstreamID != "up-b" {
		t.Fatalf("反向路由不应包含未选来源：%+v", entry.Candidates)
	}
}

func TestInvokeToolRechecksUpstreamAccessBeforeDispatch(t *testing.T) {
	cache := &invFakeCache{tools: map[string][]domain.ToolDef{"up-a": {{OriginalName: "read", Name: "read"}}}}
	svc := invNewService(cache,
		&invFakeUpstreams{upstreams: []domain.Upstream{invEnabledUpstream("up-a", 0)}},
		&invFakeAliases{byUpstream: map[string][]domain.AliasRule{}},
		&invFakeMCPFilters{byUpstream: map[string][]domain.FilterRule{}},
		&invFakeAPIKeyFilters{byAPIKey: map[string][]domain.FilterRule{}},
	)
	invoker := &invRecordingInvoker{}
	svc.SetInvoker(invoker)
	svc.SetUpstreamAccessAuthorizer(selectedUpstreamAuthorizer{allowed: "up-a", denyDispatch: true})
	if _, err := svc.InvokeTool(context.Background(), "key-1", "read", json.RawMessage(`{}`)); err == nil {
		t.Fatal("调用前上游权限复核应拒绝")
	}
	if invoker.called {
		t.Fatal("上游权限拒绝后不得转发调用")
	}
}
