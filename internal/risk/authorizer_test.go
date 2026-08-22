package risk

import (
	"context"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

type testProfiles map[string]Profile

func (p testProfiles) RiskProfile(_ context.Context, id string) (Profile, error) { return p[id], nil }

type testAssessments map[string]Assessment

func (a testAssessments) Get(_ context.Context, up, name string) (Assessment, error) {
	item, ok := a[up+":"+name]
	if !ok {
		return Assessment{}, domain.NewError(domain.CodeNotFound, "missing")
	}
	return item, nil
}
func (a testAssessments) ListByUpstream(_ context.Context, up string) ([]Assessment, error) {
	out := []Assessment{}
	for _, item := range a {
		if item.UpstreamID == up {
			out = append(out, item)
		}
	}
	return out, nil
}

func TestAuthorizerFiltersSourcesBeforeAggregation(t *testing.T) {
	assessments := testAssessments{
		"up:read":    {UpstreamID: "up", OriginalName: "read", Status: StatusRated, AILevel: LevelLow, Floor: LevelLow},
		"up:write":   {UpstreamID: "up", OriginalName: "write", Status: StatusRated, AILevel: LevelMedium, Floor: LevelMedium},
		"up:blocked": {UpstreamID: "up", OriginalName: "blocked", Status: StatusRated, AILevel: LevelBlocked, Floor: LevelBlocked},
	}
	a := NewAuthorizer(testProfiles{"key": ProfileReadonly}, assessments)
	got, err := a.FilterSources(context.Background(), "key", "up", []domain.ToolDef{{OriginalName: "read"}, {OriginalName: "write"}, {OriginalName: "missing"}, {OriginalName: "blocked"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].OriginalName != "read" {
		t.Fatalf("readonly 过滤结果错误: %#v", got)
	}
}

func TestAuthorizerLegacyBypassesRiskCatalog(t *testing.T) {
	a := NewAuthorizer(testProfiles{"key": ProfileLegacy}, testAssessments{})
	tools := []domain.ToolDef{{OriginalName: "unknown"}}
	got, err := a.FilterSources(context.Background(), "key", "up", tools)
	if err != nil || len(got) != 1 {
		t.Fatalf("legacy 应保持历史可见性: got=%v err=%v", got, err)
	}
}
