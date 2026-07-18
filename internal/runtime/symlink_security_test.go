package runtime

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func makeDirSymlink(t *testing.T, target, link string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// Windows 创建符号链接可能需要开发者模式/管理员权限。
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		return
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestFSBrowseRejectsIntermediateSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.MkdirAll(secret, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	makeDirSymlink(t, outside, link)
	escape := filepath.Join(link, "secret")

	if _, err := ListBrowseDir(escape, []string{root}, BrowseModeDirectory, 10); err == nil {
		t.Fatal("intermediate symlink escape should fail")
	}
	st, err := StatBrowsePath(escape, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if st.Allowed {
		t.Fatalf("escaped stat should not be allowed: %+v", st)
	}
}

func TestDirectoryLaunchRejectsEntrySymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "evil.py")
	if err := os.WriteFile(outsideFile, []byte("print(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "main.py")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := InspectDirectoryLaunch(root, DefaultPolicy()); err == nil {
		t.Fatal("symlinked entry outside root should fail")
	}
}

func TestMissingPathRejectsSymlinkAncestorEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "link")
	makeDirSymlink(t, outside, link)
	if MissingPathWithinRoots(filepath.Join(link, "future"), []string{root}) {
		t.Fatal("missing child below escaping symlink must be rejected")
	}
}
