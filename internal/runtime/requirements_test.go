package runtime

import (
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
		"docker":           {"docker"},
		"mybin":            nil,
	}
	for in, want := range cases {
		got := InferToolsFromCommand(in)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%q: got %v want %v", in, got, want)
		}
	}
}

func TestResolveEffectiveToolsManualAndAuto(t *testing.T) {
	t.Parallel()
	// auto from npx
	eff, sug := ResolveEffectiveTools("npx", RuntimeRequirements{Mode: RequirementsAuto}, nil)
	if len(sug) != 2 || eff[0] != "node" {
		t.Fatalf("auto npx eff=%v sug=%v", eff, sug)
	}
	// manual overrides
	eff, _ = ResolveEffectiveTools("npx", RuntimeRequirements{
		Mode:  RequirementsManual,
		Tools: []string{"docker"},
	}, nil)
	if len(eff) != 1 || eff[0] != "docker" {
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
	hasInstall := false
	for _, a := range res.Actions {
		if a.Type == "install" && a.PackageID == "node-22.14.0" {
			hasInstall = true
		}
	}
	if !hasInstall {
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

type errNotFound string

func (e errNotFound) Error() string { return string(e) }
