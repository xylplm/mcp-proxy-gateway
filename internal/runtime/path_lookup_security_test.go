package runtime

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// isolateFromImageRuntimes 把 PATH 指向一个空目录，使 imageRuntimeRoots() 为空。
// 严格档现在允许回落到镜像内置解释器目录，本组测试要断言的是「越界一律拒绝」，
// 必须排除宿主真实 node/python 参与解析，否则断言会变成在验证回落分支。
func isolateFromImageRuntimes(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func TestResolveCommandStrictRuntimeRejectsSymlinkEscape(t *testing.T) {
	isolateFromImageRuntimes(t)
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
	isolateFromImageRuntimes(t)
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

// 运行时改由镜像内置后，严格档必须能解析到镜像自带的解释器，
// 否则严格档在默认镜像上等于不可用（回归防护）。
func TestResolveCommandStrictRuntimeAcceptsImageProvidedInterpreter(t *testing.T) {
	imageBin := t.TempDir()
	name := "node"
	if runtime.GOOS == "windows" {
		name = "node.exe"
	}
	exe := filepath.Join(imageBin, name)
	if err := os.WriteFile(exe, []byte("image"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", imageBin)

	// 运行时卷存在但为空：模拟用户没往 bin 里放任何东西的默认状态。
	volumeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(volumeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCommandStrictRuntime("node", []string{volumeBin})
	if err != nil {
		t.Fatalf("严格档应接受镜像内置解释器：%v", err)
	}
	want, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// 卷内同名文件优先于镜像版本，保证 runtime/bin 仍是可用的覆盖入口。
func TestResolveCommandStrictRuntimePrefersVolumeOverImage(t *testing.T) {
	name := "node"
	if runtime.GOOS == "windows" {
		name = "node.exe"
	}
	imageBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(imageBin, name), []byte("image"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", imageBin)

	volumeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(volumeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	volumeExe := filepath.Join(volumeBin, name)
	if err := os.WriteFile(volumeExe, []byte("volume"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCommandStrictRuntime("node", []string{volumeBin})
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(volumeExe)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("卷内应优先：got %q want %q", got, want)
	}
}

// 受信目录之外的绝对路径必须拒绝：这是严格档 path-only 的核心约束。
func TestResolveCommandStrictRuntimeRejectsUntrustedAbsolutePath(t *testing.T) {
	imageBin := t.TempDir()
	name := "node"
	if runtime.GOOS == "windows" {
		name = "node.exe"
	}
	if err := os.WriteFile(filepath.Join(imageBin, name), []byte("image"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", imageBin)

	volumeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(volumeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(outside, []byte("outside"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveCommandStrictRuntime(outside, []string{volumeBin}); err == nil {
		t.Fatal("受信目录之外的绝对路径必须被拒绝")
	}
}
