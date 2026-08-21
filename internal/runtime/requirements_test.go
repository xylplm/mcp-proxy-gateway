package runtime

import (
	"slices"
	"strings"
	"testing"
)

func TestInferToolsFromCommand(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"npx":              {"node", "npx"},
		"node":             {"node"},
		"/usr/bin/python3": {"python3"},
		"uvx":              {"uv", "uvx"},
		// docker 已不是受管/可探测工具，与任意未知命令一样推断不出依赖。
		"docker": nil,
		"mybin":  nil,
	}
	for in, want := range cases {
		got := InferToolsFromCommand(in)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%q: got %v want %v", in, got, want)
		}
	}
}

func TestKnownToolsExposeInferenceMetadata(t *testing.T) {
	tools := KnownTools()
	var node, npx *KnownTool
	for i := range tools {
		switch tools[i].Name {
		case "node":
			node = &tools[i]
		case "npx":
			npx = &tools[i]
		}
	}
	if node == nil || npx == nil {
		t.Fatal("node and npx metadata required")
	}
	if !containsString(node.InferFrom, "npx") || !containsString(npx.InferFrom, "npx") {
		t.Fatalf("npx inference metadata incomplete: node=%v npx=%v", node.InferFrom, npx.InferFrom)
	}
	if !containsString(node.TemplateRuntimes, "node") {
		t.Fatalf("node template metadata missing: %v", node.TemplateRuntimes)
	}
}

func containsString(items []string, want string) bool {
	return slices.Contains(items, want)
}

func TestResolveEffectiveToolsManualAndAuto(t *testing.T) {
	t.Parallel()
	// auto from npx
	eff, sug := ResolveEffectiveTools("npx", RuntimeRequirements{Mode: RequirementsAuto}, nil)
	if len(sug) != 2 || eff[0] != "node" {
		t.Fatalf("auto npx eff=%v sug=%v", eff, sug)
	}
	// manual overrides：显式声明覆盖自动推断（python3 不会由 npx 推断出来）
	eff, _ = ResolveEffectiveTools("npx", RuntimeRequirements{
		Mode:  RequirementsManual,
		Tools: []string{"python3"},
	}, nil)
	if len(eff) != 1 || eff[0] != "python3" {
		t.Fatalf("manual=%v", eff)
	}
	// absolute-like command: empty infer, fallback saved tools in auto
	eff, _ = ResolveEffectiveTools("D:/app/mcp.exe", RuntimeRequirements{
		Mode:  RequirementsAuto,
		Tools: []string{"node"},
	}, nil)
	if len(eff) != 1 || eff[0] != "node" {
		t.Fatalf("fallback=%v", eff)
	}
}

func TestValidateRequirements(t *testing.T) {
	t.Parallel()
	_, err := ValidateRequirements(map[string]any{
		"mode":  "manual",
		"tools": []any{"node", "npx"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ValidateRequirements(map[string]any{
		"mode":  "manual",
		"tools": []any{"bash"},
	})
	if err == nil {
		t.Fatal("bash should be rejected")
	}
}

func TestEvaluatePreflightRemoteReady(t *testing.T) {
	t.Parallel()
	res := EvaluatePreflight(PreflightRequest{Transport: "sse", Command: ""}, DefaultPolicy(), "", nil)
	if !res.Ready {
		t.Fatal("remote should be ready")
	}
}

func TestEvaluatePreflightMissingTool(t *testing.T) {
	t.Parallel()
	res := EvaluatePreflight(PreflightRequest{
		Transport: "stdio",
		Command:   "npx",
		Requirements: &RuntimeRequirements{
			Mode:  RequirementsManual,
			Tools: []string{"node", "npx"},
		},
	}, DefaultPolicy(), "", func(string) (string, error) {
		return "", errNotFound("missing")
	})
	if res.Ready {
		t.Fatal("should not be ready")
	}
	if len(res.Items) != 2 {
		t.Fatalf("items=%d", len(res.Items))
	}
	// 运行时由镜像提供，缺工具只能引导用户去运行环境页排查，没有一键安装动作。
	hasRuntimeGuide := false
	for _, a := range res.Actions {
		if a.Type == "install" {
			t.Fatalf("managed install actions must be gone: %v", res.Actions)
		}
		if a.Type == "open_runtime" {
			hasRuntimeGuide = true
		}
	}
	if !hasRuntimeGuide {
		t.Fatalf("actions=%v", res.Actions)
	}
}

func TestEvaluatePreflightCommandDenied(t *testing.T) {
	t.Parallel()
	res := EvaluatePreflight(PreflightRequest{
		Transport: "stdio",
		Command:   "bash",
	}, DefaultPolicy(), "", func(string) (string, error) { return "/bin/true", nil })
	if res.CommandAllowed || res.Ready {
		t.Fatalf("bash must fail policy: %+v", res)
	}
}

// 多个工具同时缺失时只给一条排查引导，不再按包分裂出多条动作。
func TestEvaluatePreflightEmitsSingleRuntimeGuideForMultipleMissingTools(t *testing.T) {
	t.Parallel()
	res := EvaluatePreflight(PreflightRequest{
		Transport: "stdio",
		Command:   "npx",
		Requirements: &RuntimeRequirements{
			Mode:  RequirementsManual,
			Tools: []string{"uv", "node", "npx"},
		},
	}, DefaultPolicy(), "", func(string) (string, error) {
		return "", errNotFound("missing")
	})
	guides := 0
	for _, a := range res.Actions {
		if a.Type == "open_runtime" {
			guides++
		}
	}
	if guides != 1 {
		t.Fatalf("expected exactly one runtime guide action, got %v", res.Actions)
	}
}

func TestServicePreflightCacheIsInstanceScoped(t *testing.T) {
	t.Parallel()
	policy := func() Policy { return DefaultPolicy() }
	dataDir := func() string { return "" }
	runtimeDir := func() string { return "" }
	req := PreflightRequest{Transport: "sse"}

	firstService := NewService(policy, dataDir, runtimeDir)
	if got := firstService.Preflight(req); got.Cached {
		t.Fatal("first preflight on a service must not use a cache entry")
	}
	if got := firstService.Preflight(req); !got.Cached {
		t.Fatal("second preflight on the same service should use its cache")
	}

	secondService := NewService(policy, dataDir, runtimeDir)
	if got := secondService.Preflight(req); got.Cached {
		t.Fatal("a different service must not observe another service's cache")
	}
}

func TestPreflightCacheKeyIncludesIsolationCapability(t *testing.T) {
	t.Parallel()
	req := PreflightRequest{Transport: "stdio", Command: "node"}
	policy := DefaultPolicy()
	withIsolation := preflightCacheKey(req, "/data/runtime", policy, true)
	withoutIsolation := preflightCacheKey(req, "/data/runtime", policy, false)
	if withIsolation == withoutIsolation {
		t.Fatal("preflight cache key must distinguish isolation capability")
	}
}

type errNotFound string

func (e errNotFound) Error() string { return string(e) }
