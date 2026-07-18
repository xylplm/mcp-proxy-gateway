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

func TestValidateConnParamsStrictRequiresCwdAndRoots(t *testing.T) {
	t.Cleanup(func() { SetPolicyProvider(nil) })
	SetPolicyProvider(func() runtime.Policy { return runtime.DefaultPolicy() })

	err := ValidateConnParams(domain.UpstreamConfig{
		Name:      "strict-miss",
		Transport: domain.TransportStdio,
		ConnParams: map[string]any{
			ParamCommand: "node",
			ParamSecurityProfile: map[string]any{
				"mode": "strict",
			},
		},
	})
	if err == nil {
		t.Fatal("strict without cwd/roots must fail")
	}
}

func TestValidateConnParamsUnrestrictedRequiresNote(t *testing.T) {
	t.Cleanup(func() { SetPolicyProvider(nil) })
	SetPolicyProvider(func() runtime.Policy { return runtime.DefaultPolicy() })

	err := ValidateConnParams(domain.UpstreamConfig{
		Name:      "open",
		Transport: domain.TransportStdio,
		ConnParams: map[string]any{
			ParamCommand: "node",
			ParamSecurityProfile: map[string]any{
				"mode": "unrestricted",
			},
		},
	})
	if err == nil {
		t.Fatal("unrestricted without note must fail")
	}

	err = ValidateConnParams(domain.UpstreamConfig{
		Name:      "open-ok",
		Transport: domain.TransportStdio,
		ConnParams: map[string]any{
			ParamCommand: "node",
			ParamSecurityProfile: map[string]any{
				"mode": "unrestricted",
				"note": "lab",
			},
		},
	})
	if err != nil {
		t.Fatalf("unrestricted with note should pass: %v", err)
	}
}

func TestValidateConnParamsRejectsInvalidSecurityProfileType(t *testing.T) {
	t.Cleanup(func() { SetPolicyProvider(nil) })
	SetPolicyProvider(func() runtime.Policy { return runtime.DefaultPolicy() })

	err := ValidateConnParams(domain.UpstreamConfig{
		Name:      "bad-profile",
		Transport: domain.TransportStdio,
		ConnParams: map[string]any{
			ParamCommand:         "node",
			ParamSecurityProfile: "strict",
		},
	})
	if err == nil {
		t.Fatal("non-object securityProfile must fail")
	}
}

func TestValidateConnParamsRemoteUnaffectedBySecurityProfile(t *testing.T) {
	t.Cleanup(func() { SetPolicyProvider(nil) })
	SetPolicyProvider(func() runtime.Policy { return runtime.DefaultPolicy() })

	err := ValidateConnParams(domain.UpstreamConfig{
		Name:      "remote",
		Transport: domain.TransportSSE,
		ConnParams: map[string]any{
			ParamURL: "https://example.com/mcp",
			// 远程即使夹带 securityProfile 也不应导致传输校验失败（字段被忽略）。
			ParamSecurityProfile: map[string]any{"mode": "strict"},
		},
	})
	if err != nil {
		t.Fatalf("remote must ignore securityProfile: %v", err)
	}
}

func TestValidateConnParamsStrictNpxPackageAllowlist(t *testing.T) {
	t.Cleanup(func() { SetPolicyProvider(nil) })
	SetPolicyProvider(func() runtime.Policy { return runtime.DefaultPolicy() })
	root := t.TempDir()

	// 白名单内模板包：通过
	err := ValidateConnParams(domain.UpstreamConfig{
		Name:      "fs",
		Transport: domain.TransportStdio,
		ConnParams: map[string]any{
			ParamCommand: "npx",
			ParamArgs:    []string{"-y", "@modelcontextprotocol/server-filesystem", root},
			ParamCWD:     root,
			ParamSecurityProfile: map[string]any{
				"mode": "strict",
				"fileAccess": map[string]any{
					"mode":  "allowlist",
					"paths": []any{root},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("whitelisted npx package should pass: %v", err)
	}

	// 白名单外：拒绝
	err = ValidateConnParams(domain.UpstreamConfig{
		Name:      "evil",
		Transport: domain.TransportStdio,
		ConnParams: map[string]any{
			ParamCommand: "npx",
			ParamArgs:    []string{"-y", "not-in-allowlist-mcp"},
			ParamCWD:     root,
			ParamSecurityProfile: map[string]any{
				"mode": "strict",
				"fileAccess": map[string]any{
					"mode":  "allowlist",
					"paths": []any{root},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("non-allowlisted package must fail in strict")
	}
}
