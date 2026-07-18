package runtime

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestApplySandboxNoPanic(t *testing.T) {
	t.Parallel()
	ApplySandbox(nil, SandboxOptions{Enabled: true})
	cmd := exec.Command("true")
	ApplySandbox(cmd, SandboxOptions{Enabled: false})
	ApplySandbox(cmd, SandboxOptions{
		Enabled:      true,
		SecurityMode: SecurityModeStrict,
		FileRoots:    []string{t.TempDir()},
		NetworkMode:  NetworkAccessDeny,
	})
	cap := DescribeSandbox()
	if cap.Platform == "" {
		t.Fatal("platform empty")
	}
	if cap.Description == "" {
		t.Fatal("description empty")
	}
	if cap.IsolationBackend == "" {
		t.Fatal("backend empty")
	}
}

func TestWrapStrictWithBwrapWhenAvailable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap wrapping is linux-only")
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil || bwrap == "" {
		t.Skip("bubblewrap not installed")
	}
	root := t.TempDir()
	cmd := exec.Command("/bin/true", "arg1")
	ApplySandbox(cmd, SandboxOptions{
		Enabled:      true,
		SecurityMode: SecurityModeStrict,
		FileRoots:    []string{root},
		CWD:          root,
		NetworkMode:  NetworkAccessDeny,
		RuntimeDir:   root,
	})
	if filepath.Base(cmd.Path) != "bwrap" && !strings.HasSuffix(cmd.Path, "/bwrap") {
		t.Fatalf("expected bwrap wrap, path=%s args=%v", cmd.Path, cmd.Args)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "--unshare-net") {
		t.Fatalf("deny network should unshare-net: %s", joined)
	}
	if !strings.Contains(joined, "--bind") {
		t.Fatalf("file roots should bind: %s", joined)
	}
	if !strings.Contains(joined, "/bin/true") {
		t.Fatalf("original command missing: %s", joined)
	}
}

func TestIsolationAvailableMatchesDescribe(t *testing.T) {
	t.Parallel()
	caps := DescribeSandbox()
	if IsolationAvailable() != (caps.FilesystemIsolationSupported || caps.NetworkIsolationSupported) {
		t.Fatalf("IsolationAvailable mismatch: caps=%+v", caps)
	}
}
