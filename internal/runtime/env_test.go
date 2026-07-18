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
