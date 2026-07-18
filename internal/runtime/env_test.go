package runtime

import (
	"strings"
	"testing"
)

func TestBuildChildEnvScrubsParentSecretsKeepsPath(t *testing.T) {
	t.Parallel()
	parent := []string{
		"PATH=/usr/bin",
		"HOME=/home/mpg",
		"MPG_PG_DSN=postgres://secret",
		"MPG_REDIS_ADDR=redis:6379",
		"OPENAI_API_KEY=sk-test",
		"MY_CUSTOM_SECRET=abc",
		"NORMAL_FLAG=1",
	}
	out := BuildChildEnv(parent, nil, Policy{StdioEnabled: true})
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "PATH=/usr/bin") {
		t.Fatalf("PATH must be preserved: %v", out)
	}
	if !strings.Contains(joined, "HOME=/home/mpg") {
		t.Fatalf("HOME must be preserved: %v", out)
	}
	if !strings.Contains(joined, "NORMAL_FLAG=1") {
		t.Fatalf("normal vars must be preserved: %v", out)
	}
	for _, bad := range []string{"MPG_PG_DSN", "MPG_REDIS_ADDR", "OPENAI_API_KEY", "MY_CUSTOM_SECRET"} {
		if strings.Contains(joined, bad+"=") {
			t.Fatalf("sensitive %s must be scrubbed: %v", bad, out)
		}
	}
}

func TestBuildChildEnvUserOverridesAndExplicitSecretsAllowed(t *testing.T) {
	t.Parallel()
	parent := []string{
		"PATH=/usr/bin",
		"TOKEN=parent-token",
		"API_KEY=parent",
	}
	user := map[string]string{
		"TOKEN":   "mcp-user-token",
		"API_KEY": "from-upstream",
		"FOO":     "bar",
	}
	out := BuildChildEnv(parent, user, Policy{StdioEnabled: true})
	env := map[string]string{}
	for _, e := range out {
		k, v, ok := splitEnvEntry(e)
		if ok {
			env[k] = v
		}
	}
	if env["PATH"] != "/usr/bin" {
		t.Fatalf("PATH=%q", env["PATH"])
	}
	if env["TOKEN"] != "mcp-user-token" {
		t.Fatalf("user TOKEN should override/be kept, got %q", env["TOKEN"])
	}
	if env["API_KEY"] != "from-upstream" {
		t.Fatalf("user API_KEY should be kept, got %q", env["API_KEY"])
	}
	if env["FOO"] != "bar" {
		t.Fatalf("FOO=%q", env["FOO"])
	}
}

func TestBuildChildEnvStrictModeMinimalInherit(t *testing.T) {
	t.Parallel()
	parent := []string{
		"PATH=/usr/bin",
		"HOME=/home/mpg",
		"RANDOM_APP_FLAG=1",
		"LD_PRELOAD=/tmp/x.so",
	}
	user := map[string]string{
		"LD_PRELOAD":   "/evil.so",
		"NODE_OPTIONS": "--require ./x",
		"MCP_TOKEN":    "ok",
	}
	out := BuildChildEnvWithOptions(parent, user, DefaultPolicy(), ChildEnvOptions{
		Mode:       SecurityModeStrict,
		RuntimeDir: "/data/runtime",
	}, "/data/runtime/bin")
	env := map[string]string{}
	for _, e := range out {
		k, v, ok := splitEnvEntry(e)
		if ok {
			env[k] = v
		}
	}
	if env["HOME"] != "/home/mpg" {
		t.Fatalf("HOME should inherit: %v", env["HOME"])
	}
	if _, ok := env["RANDOM_APP_FLAG"]; ok {
		t.Fatalf("strict must not inherit random parent env: %v", out)
	}
	if _, ok := env["LD_PRELOAD"]; ok {
		t.Fatalf("dangerous user env must be dropped: %v", out)
	}
	if _, ok := env["NODE_OPTIONS"]; ok {
		t.Fatalf("NODE_OPTIONS must be dropped: %v", out)
	}
	if env["MCP_TOKEN"] != "ok" {
		t.Fatalf("benign user env must remain: %v", env["MCP_TOKEN"])
	}
	if env["MPG_STDIO_SECURITY_MODE"] != "strict" {
		t.Fatalf("mode signal missing: %v", env["MPG_STDIO_SECURITY_MODE"])
	}
	if !strings.Contains(env["PATH"], "/data/runtime/bin") {
		t.Fatalf("runtime path prefix missing: %q", env["PATH"])
	}
}

func TestBuildChildEnvExtraPrefix(t *testing.T) {
	t.Parallel()
	parent := []string{"PATH=/bin", "CORP_INTERNAL_KEY=1", "OK=1"}
	out := BuildChildEnv(parent, nil, Policy{
		StdioEnabled:              true,
		ExtraSensitiveEnvPrefixes: []string{"CORP_"},
	})
	joined := strings.Join(out, "\n")
	if strings.Contains(joined, "CORP_INTERNAL_KEY=") {
		t.Fatalf("extra prefix should scrub: %v", out)
	}
	if !strings.Contains(joined, "OK=1") {
		t.Fatalf("OK should remain: %v", out)
	}
}

func TestBuildChildEnvPrependsRuntimePath(t *testing.T) {
	t.Parallel()
	parent := []string{"PATH=/usr/bin", "MPG_PG_DSN=secret"}
	rtBin := "/data/runtime/bin"
	out := BuildChildEnv(parent, nil, Policy{StdioEnabled: true}, rtBin)
	env := map[string]string{}
	for _, e := range out {
		k, v, ok := splitEnvEntry(e)
		if ok {
			env[k] = v
		}
	}
	if !strings.HasPrefix(env["PATH"], rtBin) {
		t.Fatalf("PATH should start with runtime bin, got %q", env["PATH"])
	}
	if strings.Contains(strings.Join(out, "\n"), "MPG_PG_DSN=") {
		t.Fatalf("secrets must stay scrubbed: %v", out)
	}
	// 用户显式 PATH 覆盖
	out2 := BuildChildEnv(parent, map[string]string{"PATH": "/only"}, Policy{StdioEnabled: true}, rtBin)
	env2 := map[string]string{}
	for _, e := range out2 {
		k, v, ok := splitEnvEntry(e)
		if ok {
			env2[k] = v
		}
	}
	if env2["PATH"] != "/only" {
		t.Fatalf("user PATH should win, got %q", env2["PATH"])
	}
}
