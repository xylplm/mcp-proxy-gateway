package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

type fakeRuleValidator struct{}

func (fakeRuleValidator) ValidateAlias(r domain.AliasRule) error {
	return domain.NewRuleEngine().ValidateAlias(r)
}

func (fakeRuleValidator) ValidateFilter(r domain.FilterRule) error {
	return domain.NewRuleEngine().ValidateFilter(r)
}

func (fakeRuleValidator) ValidateToolPolicy(r domain.ToolPolicyRule) error {
	return domain.NewRuleEngine().ValidateToolPolicy(r)
}

type fakeToolPolicyStore struct {
	rules     []domain.ToolPolicyRule
	created   domain.ToolPolicyRule
	updated   domain.ToolPolicyRule
	enabledID string
	enabled   *bool
	deleted   string
}

func (f *fakeToolPolicyStore) Create(_ context.Context, rule domain.ToolPolicyRule) (domain.ToolPolicyRule, error) {
	rule.ID = "policy-new"
	f.created = rule
	f.rules = append(f.rules, rule)
	return rule, nil
}

func (f *fakeToolPolicyStore) Get(_ context.Context, id string) (domain.ToolPolicyRule, error) {
	for _, rule := range f.rules {
		if rule.ID == id {
			return rule, nil
		}
	}
	return domain.ToolPolicyRule{}, domain.NewError(domain.CodeNotFound, "工具策略规则不存在")
}

func (f *fakeToolPolicyStore) List(context.Context) ([]domain.ToolPolicyRule, error) {
	return f.rules, nil
}

func (f *fakeToolPolicyStore) Count(context.Context) (int, error) {
	return len(f.rules), nil
}

func (f *fakeToolPolicyStore) Update(_ context.Context, rule domain.ToolPolicyRule) (domain.ToolPolicyRule, error) {
	f.updated = rule
	return rule, nil
}

func (f *fakeToolPolicyStore) SetEnabled(_ context.Context, id string, enabled bool) error {
	f.enabledID = id
	f.enabled = &enabled
	return nil
}

func (f *fakeToolPolicyStore) Delete(_ context.Context, id string) error {
	f.deleted = id
	return nil
}

func TestToolPolicyCRUDRoutes(t *testing.T) {
	store := &fakeToolPolicyStore{rules: []domain.ToolPolicyRule{{
		ID:              "policy-1",
		Pattern:         "search",
		Enabled:         true,
		RoutingStrategy: domain.ToolRoutingRoundRobin,
		CacheEnabled:    true,
		CacheTTLSeconds: 30,
		RiskTags:        []string{"外发"},
	}}}
	e := newTestEngine(Deps{RuleValidator: fakeRuleValidator{}, ToolPolicyStore: store})

	w := doJSON(e, http.MethodGet, "/api/admin/tool-policies", "")
	if w.Code != http.StatusOK {
		t.Fatalf("列表期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	var list struct {
		ToolPolicies []domain.ToolPolicyRule `json:"toolPolicies"`
	}
	unmarshalData(t, w, &list)
	if len(list.ToolPolicies) != 1 || list.ToolPolicies[0].RiskTags[0] != "外发" {
		t.Fatalf("工具策略列表不符合预期：%+v", list)
	}

	w = doJSON(e, http.MethodPost, "/api/admin/tool-policies", `{"pattern":"read_.+","isRegex":true,"enabled":true,"routingStrategy":"priority_fill","cacheEnabled":true,"cacheTtlSeconds":15,"riskTags":["只读缓存"]}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("创建期望 HTTP 201，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if store.created.Pattern != "read_.+" || store.created.RoutingStrategy != domain.ToolRoutingPriorityFill || store.created.CacheTTLSeconds != 15 {
		t.Fatalf("创建参数未正确传递：%+v", store.created)
	}

	w = doJSON(e, http.MethodPut, "/api/admin/tool-policies/policy-1", `{"pattern":"write","enabled":false,"sortOrder":7,"riskTags":["写入"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("更新期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if store.updated.ID != "policy-1" || store.updated.Pattern != "write" || store.updated.SortOrder != 7 {
		t.Fatalf("更新参数未正确传递：%+v", store.updated)
	}

	w = doJSON(e, http.MethodPost, "/api/admin/tool-policies/policy-1/disable", "")
	if w.Code != http.StatusOK {
		t.Fatalf("停用期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if store.enabledID != "policy-1" || store.enabled == nil || *store.enabled {
		t.Fatalf("停用未正确传递：id=%q enabled=%v", store.enabledID, store.enabled)
	}

	w = doJSON(e, http.MethodDelete, "/api/admin/tool-policies/policy-1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("删除期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if store.deleted != "policy-1" {
		t.Fatalf("删除目标不符合预期：%q", store.deleted)
	}
}

func TestCreateToolPolicyRejectsInvalidCacheTTL(t *testing.T) {
	e := newTestEngine(Deps{RuleValidator: fakeRuleValidator{}, ToolPolicyStore: &fakeToolPolicyStore{}})

	w := doJSON(e, http.MethodPost, "/api/admin/tool-policies", `{"pattern":"read","enabled":true,"cacheEnabled":true,"cacheTtlSeconds":0}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 TTL 期望 HTTP 400，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	_, _, fields := parseErrorEnvelope(t, w)
	if fields["cacheTtlSeconds"] == "" {
		t.Fatalf("应返回 cacheTtlSeconds 字段错误：%+v", fields)
	}
}
