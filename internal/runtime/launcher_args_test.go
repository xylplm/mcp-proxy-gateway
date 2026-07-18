package runtime

import "testing"

func TestExtractNpxTarget(t *testing.T) {
	t.Parallel()
	target, ok, err := ExtractLauncherTarget("npx", []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"})
	if err != nil || !ok || target != "@modelcontextprotocol/server-filesystem" {
		t.Fatalf("got %q ok=%v err=%v", target, ok, err)
	}
	target, ok, err = ExtractLauncherTarget("npx", []string{"--package", "foo@1.2.3", "bar"})
	if err != nil || !ok || target != "foo" {
		t.Fatalf("package flag: %q ok=%v err=%v", target, ok, err)
	}
	if _, _, err := ExtractLauncherTarget("npx", []string{"-c", "echo hi"}); err == nil {
		t.Fatal("npx -c must fail")
	}
	if _, _, err := ExtractLauncherTarget("npx", []string{"./local-pkg"}); err == nil {
		t.Fatal("local path must fail")
	}
	if _, _, err := ExtractLauncherTarget("npx", []string{"https://evil.example/pkg.tgz"}); err == nil {
		t.Fatal("url must fail")
	}
}

func TestExtractUvxTarget(t *testing.T) {
	t.Parallel()
	target, ok, err := ExtractLauncherTarget("uvx", []string{"ruff@0.1.0", "check"})
	if err != nil || !ok || target != "ruff" {
		t.Fatalf("got %q ok=%v err=%v", target, ok, err)
	}
	target, ok, err = ExtractLauncherTarget("uvx", []string{"--from", "httpx", "httpx"})
	if err != nil || !ok || target != "httpx" {
		t.Fatalf("from: %q ok=%v err=%v", target, ok, err)
	}
	if _, _, err := ExtractLauncherTarget("uvx", []string{"install", "ruff"}); err == nil {
		t.Fatal("uvx install subcommand must fail")
	}
}

func TestValidateStrictLauncherTargetAllowlist(t *testing.T) {
	t.Parallel()
	allow := DefaultStrictPackageAllowlist()
	// 模板常用包应通过
	if err := ValidateStrictLauncherTarget("npx", []string{"-y", "@modelcontextprotocol/server-memory"}, allow); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStrictLauncherTarget("npx", []string{"-y", "firecrawl-mcp"}, allow); err != nil {
		t.Fatal(err)
	}
	// 未在白名单
	if err := ValidateStrictLauncherTarget("npx", []string{"-y", "left-pad"}, allow); err == nil {
		t.Fatal("unknown package must fail")
	}
	// 非 launcher 不校验
	if err := ValidateStrictLauncherTarget("node", []string{"app.js"}, allow); err != nil {
		t.Fatal(err)
	}
	// 上游追加
	if err := ValidateStrictLauncherTarget("npx", []string{"-y", "my-custom-mcp"}, []string{"my-custom-mcp"}); err != nil {
		t.Fatal(err)
	}
}

func TestPackageAllowedScopeWildcard(t *testing.T) {
	t.Parallel()
	allow := normalizePackageAllowlist([]string{"@modelcontextprotocol/*"})
	if !packageAllowed("@modelcontextprotocol/server-fetch", allow) {
		t.Fatal("scope wildcard should match")
	}
	if packageAllowed("@other/server-fetch", allow) {
		t.Fatal("other scope must not match")
	}
}

func TestStrictModeEnforcesPackageAllowlist(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := DefaultPolicy()
	eff := ResolveEffectiveSecurity(p, SecurityProfile{
		Mode: SecurityModeStrict,
		FileAccess: FileAccessPolicy{
			Mode:  FileAccessAllowlist,
			Paths: []string{root},
		},
	}, root)
	if err := ValidateEffectiveSecurityWithCommand(eff, root, "npx", []string{"-y", "@modelcontextprotocol/server-filesystem", root}); err != nil {
		t.Fatalf("builtin package should pass: %v", err)
	}
	if err := ValidateEffectiveSecurityWithCommand(eff, root, "npx", []string{"-y", "totally-unknown-mcp"}); err == nil {
		t.Fatal("unknown package must fail in strict")
	}
	// 上游 packageAllowlist 追加
	eff2 := ResolveEffectiveSecurity(p, SecurityProfile{
		Mode:             SecurityModeStrict,
		PackageAllowlist: []string{"totally-unknown-mcp"},
		FileAccess: FileAccessPolicy{
			Mode:  FileAccessAllowlist,
			Paths: []string{root},
		},
	}, root)
	if err := ValidateEffectiveSecurityWithCommand(eff2, root, "npx", []string{"-y", "totally-unknown-mcp"}); err != nil {
		t.Fatalf("upstream allowlist should pass: %v", err)
	}
}
