package runtime

import (
	"strings"
	"testing"
)

func TestValidateCommandDefaults(t *testing.T) {
	t.Parallel()
	policy := DefaultPolicy()

	for _, cmd := range []string{"npx", "node", "/usr/bin/python3", `C:\Tools\uvx.exe`, "docker"} {
		if err := ValidateCommand(cmd, policy); err != nil {
			t.Fatalf("command %q should be allowed: %v", cmd, err)
		}
	}
}

func TestValidateCommandDeniesShells(t *testing.T) {
	t.Parallel()
	policy := NormalizePolicy(Policy{StdioEnabled: true, CommandAllowlist: []string{"bash", "npx", "env", "busybox"}})
	// shell / 包装器即使出现在 allowlist 也必须拒绝
	for _, cmd := range []string{"bash", "/bin/bash", "cmd.exe", "powershell", "pwsh", "sh", "env", "busybox"} {
		err := ValidateCommand(cmd, policy)
		if err == nil {
			t.Fatalf("command %q must be denied", cmd)
		}
		if !strings.Contains(err.Error(), "安全") && !strings.Contains(err.Error(), "shell") {
			t.Fatalf("unexpected error for %q: %v", cmd, err)
		}
	}
}

func TestValidateCommandAllowlistAndDisabled(t *testing.T) {
	t.Parallel()
	policy := NormalizePolicy(Policy{
		StdioEnabled:     true,
		CommandAllowlist: []string{"node"},
	})
	if err := ValidateCommand("npx", policy); err == nil {
		t.Fatal("npx should be rejected by allowlist")
	}
	if err := ValidateCommand("node", policy); err != nil {
		t.Fatalf("node should pass: %v", err)
	}

	disabled := NormalizePolicy(Policy{StdioEnabled: false})
	if err := ValidateCommand("node", disabled); err == nil {
		t.Fatal("expected disabled stdio error")
	}
}

func TestCommandBaseName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"npx":                  "npx",
		"/usr/bin/python3":     "python3",
		`C:\bin\node.exe`:      "node",
		`C:\Tools\uvx.exe`:     "uvx",
		`C:/Tools/uvx.EXE`:     "uvx",
		`\\server\share\a.bat`: "a",
		"UVX.CMD":              "uvx",
		"  ":                   "",
	}
	for in, want := range cases {
		if got := CommandBaseName(in); got != want {
			t.Fatalf("CommandBaseName(%q)=%q want %q", in, got, want)
		}
	}
}
