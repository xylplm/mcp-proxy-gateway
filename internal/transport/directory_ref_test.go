package transport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/runtime"
)

func TestResolveDirectoryLaunch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.py"), []byte("print(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := runtime.DefaultPolicy()
	policy.GlobalFileRoots = []string{root}
	params := map[string]any{
		ParamLaunchMode: "directory",
		"directoryRef":  map[string]any{"root": root, "entryId": "python-main"},
		ParamCommand:    "bash",
		ParamArgs:       []any{"evil"},
	}
	cmd, args, cwd, ok, err := resolveDirectoryLaunch(params, policy, nil)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if cmd != "python3" || len(args) != 1 || cwd != root {
		t.Fatalf("cmd=%s args=%v cwd=%s", cmd, args, cwd)
	}
}

func TestResolveDirectoryLaunchRejectsBrowseOnlyRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := runtime.DefaultPolicy()
	policy.BrowseExtraRoots = []string{root}
	params := map[string]any{
		ParamLaunchMode: "directory",
		"directoryRef":  map[string]any{"root": root, "entryId": "python-main"},
	}
	if _, _, _, ok, err := resolveDirectoryLaunch(params, policy, nil); !ok || err == nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestResolveDirectoryLaunchRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := runtime.DefaultPolicy()
	policy.GlobalFileRoots = []string{t.TempDir()}
	params := map[string]any{
		ParamLaunchMode: "directory",
		"directoryRef":  map[string]any{"root": root, "entryId": "python-main"},
	}
	if _, _, _, ok, err := resolveDirectoryLaunch(params, policy, nil); !ok || err == nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}
