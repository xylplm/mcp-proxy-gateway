package runtime

import (
	"os/exec"
	"testing"
)

func TestApplySandboxNoPanic(t *testing.T) {
	t.Parallel()
	ApplySandbox(nil, SandboxOptions{Enabled: true})
	cmd := exec.Command("true")
	ApplySandbox(cmd, SandboxOptions{Enabled: false})
	ApplySandbox(cmd, SandboxOptions{Enabled: true})
	cap := DescribeSandbox()
	if cap.Platform == "" {
		t.Fatal("platform empty")
	}
	if cap.Description == "" {
		t.Fatal("description empty")
	}
}
