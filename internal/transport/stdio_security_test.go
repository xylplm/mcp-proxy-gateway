package transport

import (
	"strings"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/runtime"
)

func TestValidateConnParamsRejectsShellCommand(t *testing.T) {
	t.Cleanup(func() { SetPolicyProvider(nil) })
	SetPolicyProvider(func() runtime.Policy { return runtime.DefaultPolicy() })

	err := ValidateConnParams(domain.UpstreamConfig{
		Name:      "bad",
		Transport: domain.TransportStdio,
		ConnParams: map[string]any{
			ParamCommand: "bash",
		},
	})
	if err == nil {
		t.Fatal("expected validation error for bash")
	}
	msg := err.Error()
	if !strings.Contains(msg, "安全") && !strings.Contains(msg, "shell") {
		t.Fatalf("unexpected message: %v", err)
	}
}

func TestValidateConnParamsRejectsOutsideAllowlist(t *testing.T) {
	t.Cleanup(func() { SetPolicyProvider(nil) })
	SetPolicyProvider(func() runtime.Policy {
		return runtime.Policy{StdioEnabled: true, CommandAllowlist: []string{"node"}}
	})

	err := ValidateConnParams(domain.UpstreamConfig{
		Name:      "bad",
		Transport: domain.TransportStdio,
		ConnParams: map[string]any{
			ParamCommand: "curl",
		},
	})
	if err == nil {
		t.Fatal("expected allowlist rejection")
	}
}

func TestValidateConnParamsAcceptsNpxWithDefaultPolicy(t *testing.T) {
	t.Cleanup(func() { SetPolicyProvider(nil) })
	SetPolicyProvider(func() runtime.Policy { return runtime.DefaultPolicy() })

	err := ValidateConnParams(domain.UpstreamConfig{
		Name:      "ok",
		Transport: domain.TransportStdio,
		ConnParams: map[string]any{
			ParamCommand: "npx",
			ParamArgs:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
		},
	})
	if err != nil {
		t.Fatalf("npx should pass default policy: %v", err)
	}
}

func TestValidateConnParamsRespectsStdioDisabled(t *testing.T) {
	t.Cleanup(func() { SetPolicyProvider(nil) })
	SetPolicyProvider(func() runtime.Policy {
		return runtime.Policy{StdioEnabled: false, CommandAllowlist: runtime.DefaultCommandAllowlist()}
	})

	err := ValidateConnParams(domain.UpstreamConfig{
		Name:       "off",
		Transport:  domain.TransportStdio,
		ConnParams: map[string]any{ParamCommand: "node"},
	})
	if err == nil {
		t.Fatal("expected disabled error")
	}
}
