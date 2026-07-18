package runtime

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveCommandStrictRuntimeRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "node")
	if err := os.WriteFile(outside, []byte("outside"), 0o755); err != nil {
		t.Fatal(err)
	}
	name := "node"
	if runtime.GOOS == "windows" {
		name = "node.exe"
	}
	if err := os.Symlink(outside, filepath.Join(bin, name)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ResolveCommandStrictRuntime("node", []string{bin}); err == nil {
		t.Fatal("strict runtime must reject executable symlink escaping runtime root")
	}
}

func TestResolveCommandStrictRuntimeRejectsEscapingPrefixSymlink(t *testing.T) {
	root := t.TempDir()
	outsideBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(outsideBin, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "node"
	if runtime.GOOS == "windows" {
		name = "node.exe"
	}
	if err := os.WriteFile(filepath.Join(outsideBin, name), []byte("outside"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "bin")
	if err := os.Symlink(outsideBin, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ResolveCommandStrictRuntime("node", []string{link}); err == nil {
		t.Fatal("escaping prefix symlink must not become an allowed runtime root")
	}
}

func TestResolveCommandStrictRuntimeReturnsResolvedExecutable(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "node"
	if runtime.GOOS == "windows" {
		name = "node.exe"
	}
	exe := filepath.Join(bin, name)
	if err := os.WriteFile(exe, []byte("local"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCommandStrictRuntime("node", []string{bin})
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
