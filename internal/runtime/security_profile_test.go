package runtime

import (
	"path/filepath"
	"testing"
)

func TestNormalizeSecurityMode(t *testing.T) {
	t.Parallel()
	if got := NormalizeSecurityMode("STRICT", SecurityModeStandard); got != SecurityModeStrict {
		t.Fatalf("got %s", got)
	}
	if got := NormalizeSecurityMode("nope", SecurityModeUnrestricted); got != SecurityModeUnrestricted {
		t.Fatalf("fallback got %s", got)
	}
	if got := NormalizeSecurityMode("", ""); got != SecurityModeStandard {
		t.Fatalf("empty default got %s", got)
	}
}

func TestEffectiveCommandAllowlist(t *testing.T) {
	t.Parallel()
	p := Policy{
		StdioEnabled:             true,
		CommandAllowlist:         []string{"node", "npx", "npm", "docker", "uv"},
		StrictCommandAllowlist:   []string{"node", "npx", "uv", "uvx"},
		DefaultStdioSecurityMode: SecurityModeStandard,
	}
	std := EffectiveCommandAllowlist(p, SecurityModeStandard)
	if !containsName(std, "docker") || !containsName(std, "npm") {
		t.Fatalf("standard should keep global list: %v", std)
	}
	strict := EffectiveCommandAllowlist(p, SecurityModeStrict)
	if containsName(strict, "docker") || containsName(strict, "npm") {
		t.Fatalf("strict should drop docker/npm: %v", strict)
	}
	if !containsName(strict, "node") || !containsName(strict, "uv") {
		t.Fatalf("strict intersection: %v", strict)
	}
	unres := EffectiveCommandAllowlist(p, SecurityModeUnrestricted)
	if len(unres) != 0 {
		t.Fatalf("unrestricted denylist-only want empty, got %v", unres)
	}
}

func TestValidateCommandForSecurityModes(t *testing.T) {
	t.Parallel()
	p := DefaultPolicy()
	p.CommandAllowlist = DefaultCommandAllowlist()
	p.StrictCommandAllowlist = DefaultStrictCommandAllowlist()

	std := ResolveEffectiveSecurity(p, SecurityProfile{Mode: SecurityModeStandard}, "")
	if err := ValidateCommandForSecurity("node", p, std); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCommandForSecurity("docker", p, std); err != nil {
		t.Fatalf("docker allowed in standard: %v", err)
	}

	strict := ResolveEffectiveSecurity(p, SecurityProfile{Mode: SecurityModeStrict}, "")
	if err := ValidateCommandForSecurity("docker", p, strict); err == nil {
		t.Fatal("docker must be denied in strict")
	}
	if err := ValidateCommandForSecurity("bash", p, strict); err == nil {
		t.Fatal("bash always denied")
	}

	unres := ResolveEffectiveSecurity(p, SecurityProfile{Mode: SecurityModeUnrestricted}, "")
	if err := ValidateCommandForSecurity("my-custom-bin", p, unres); err != nil {
		t.Fatalf("custom bin should pass unrestricted denylist-only: %v", err)
	}
	if err := ValidateCommandForSecurity("bash", p, unres); err == nil {
		t.Fatal("bash still denied in unrestricted")
	}
}

func TestValidateCwdStrict(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sub := filepath.Join(root, "work")
	fa := FileAccessPolicy{Mode: FileAccessAllowlist, Paths: []string{root}}

	if err := ValidateCwdAgainstFileAccess("", fa, SecurityModeStrict); err == nil {
		t.Fatal("strict requires cwd")
	}
	if err := ValidateCwdAgainstFileAccess(sub, fa, SecurityModeStrict); err != nil {
		t.Fatalf("sub of root should pass: %v", err)
	}
	if err := ValidateCwdAgainstFileAccess(filepath.Join(root, "..", "outside"), fa, SecurityModeStrict); err == nil {
		// clean may resolve outside
		outside := filepath.Clean(filepath.Join(root, "..", "outside"))
		ok, _ := pathAllowed(outside, []string{root})
		if ok {
			t.Fatal("outside should not be allowed")
		}
	}
}

func TestDetectSelfInstallIntent(t *testing.T) {
	t.Parallel()
	if err := DetectSelfInstallIntent("npx", []string{"-y", "@modelcontextprotocol/server-fs", "/data"}); err != nil {
		t.Fatalf("normal npx args: %v", err)
	}
	if err := DetectSelfInstallIntent("node", []string{"server.js", "-g", "not-install"}); err != nil {
		t.Fatalf("node -g business flag must not trip: %v", err)
	}
	if err := DetectSelfInstallIntent("npm", []string{"install", "lodash"}); err == nil {
		t.Fatal("npm install should fail")
	}
	if err := DetectSelfInstallIntent("npm", []string{"-g", "typescript"}); err == nil {
		t.Fatal("npm -g should fail")
	}
	if err := DetectSelfInstallIntent("", []string{"install", "lodash"}); err == nil {
		t.Fatal("bare install args should fail")
	}
}

func TestUnrestrictedRequiresNote(t *testing.T) {
	t.Parallel()
	p := DefaultPolicy()
	// 上游显式完全放行：无 note 拒绝（防 API 静默落库）。
	eff := ResolveEffectiveSecurity(p, SecurityProfile{Mode: SecurityModeUnrestricted}, "")
	if err := ValidateEffectiveSecurityWithCommand(eff, "", "node", nil); err == nil {
		t.Fatal("unrestricted without note must fail")
	}
	eff.Note = "lab only"
	if err := ValidateEffectiveSecurityWithCommand(eff, "", "node", nil); err != nil {
		t.Fatal(err)
	}
	// 仅全局默认 unrestricted、上游未声明：不得误杀存量 stdio（主业务兼容）。
	p.DefaultStdioSecurityMode = SecurityModeUnrestricted
	inherited := ResolveEffectiveSecurity(p, SecurityProfile{}, "")
	if inherited.Mode != SecurityModeUnrestricted {
		t.Fatalf("mode=%s", inherited.Mode)
	}
	if inherited.RequiresAck {
		t.Fatal("inherited unrestricted must not require note")
	}
	if err := ValidateEffectiveSecurityWithCommand(inherited, "", "node", nil); err != nil {
		t.Fatalf("inherited unrestricted should connect: %v", err)
	}
}

func TestValidateIsolationRequirement(t *testing.T) {
	t.Parallel()
	policy := DefaultPolicy()
	eff := ResolveEffectiveSecurity(policy, SecurityProfile{Mode: SecurityModeStrict}, "")
	if err := ValidateIsolationRequirement(policy, eff); err != nil {
		t.Fatalf("default policy-only compatibility should pass: %v", err)
	}
	policy.StrictAllowPolicyOnly = false
	if err := ValidateIsolationRequirement(policy, eff); err == nil {
		t.Fatal("strict restrictions must fail when policy-only isolation is disabled")
	}
	unrestricted := eff
	unrestricted.FileAccess.Mode = FileAccessUnrestricted
	unrestricted.Network.Mode = NetworkAccessUnrestricted
	if err := ValidateIsolationRequirement(policy, unrestricted); err != nil {
		t.Fatalf("no requested isolation should pass: %v", err)
	}
}

func TestStrictPathOnlyRespectsPolicy(t *testing.T) {
	t.Parallel()
	p := DefaultPolicy()
	p.StrictPathOnlyRuntime = false
	eff := ResolveEffectiveSecurity(p, SecurityProfile{Mode: SecurityModeStrict}, "")
	if eff.StrictPathOnly {
		t.Fatal("strict must not force path-only when policy disables it")
	}
	p.StrictPathOnlyRuntime = true
	eff = ResolveEffectiveSecurity(p, SecurityProfile{Mode: SecurityModeStrict}, "")
	if !eff.StrictPathOnly {
		t.Fatal("strict should enable path-only when policy enables it")
	}
}

func TestParseAndValidateSecurityProfile(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"mode": "strict",
		"fileAccess": map[string]any{
			"mode":  "allowlist",
			"paths": []any{t.TempDir()},
		},
		"network": map[string]any{
			"mode":  "deny",
			"hosts": []any{},
		},
		"dependencyPolicy": "declared_only",
		"allowSelfInstall": false,
	}
	p, err := ValidateSecurityProfile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != SecurityModeStrict {
		t.Fatalf("mode=%s", p.Mode)
	}
	eff := ResolveEffectiveSecurity(DefaultPolicy(), p, p.FileAccess.Paths[0])
	if eff.Mode != SecurityModeStrict || eff.AllowSelfInstall {
		t.Fatalf("eff=%+v", eff)
	}
	if err := ValidateEffectiveSecurityWithCommand(eff, p.FileAccess.Paths[0], "node", nil); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSecurityProfileRejectsInvalidRawValues(t *testing.T) {
	t.Parallel()
	cases := map[string]any{
		"unknown mode":       map[string]any{"mode": "strcit"},
		"wrong nested type":  map[string]any{"fileAccess": "allowlist"},
		"unknown file mode":  map[string]any{"fileAccess": map[string]any{"mode": "read"}},
		"unknown network":    map[string]any{"network": map[string]any{"mode": "local"}},
		"mixed path array":   map[string]any{"fileAccess": map[string]any{"paths": []any{t.TempDir(), 1}}},
		"mixed hosts array":  map[string]any{"network": map[string]any{"hosts": []any{"example.com", true}}},
		"wrong install type": map[string]any{"allowSelfInstall": "false"},
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateSecurityProfile(raw); err == nil {
				t.Fatal("invalid raw security profile should fail")
			}
		})
	}
}

func TestRejectFilesystemRootAsAllowPath(t *testing.T) {
	t.Parallel()
	if err := validateDeclaredPath("/"); err == nil {
		// Windows root may differ; only assert unix-style when applicable
		if filepath.Separator == '/' {
			t.Fatal("root must be rejected")
		}
	}
}
