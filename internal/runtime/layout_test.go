package runtime

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveRuntimeDir(t *testing.T) {
	t.Parallel()
	if got := ResolveRuntimeDir("/data", ""); got != filepath.Clean(filepath.Join("/data", "runtime")) && got != filepath.Clean("/data/runtime") {
		// Join 在 Windows 上可能变成 \data\runtime
		if !strings.HasSuffix(strings.ReplaceAll(got, "\\", "/"), "data/runtime") {
			t.Fatalf("default: got %q", got)
		}
	}
	if got := ResolveRuntimeDir("", ""); got != "" {
		t.Fatalf("empty data: got %q", got)
	}
	if got := ResolveRuntimeDir("/data", "/custom/rt"); !strings.Contains(strings.ReplaceAll(got, "\\", "/"), "custom/rt") {
		t.Fatalf("abs override: got %q", got)
	}
	if got := ResolveRuntimeDir("/data", "rel-rt"); !strings.HasSuffix(strings.ReplaceAll(got, "\\", "/"), "data/rel-rt") {
		t.Fatalf("rel override: got %q", got)
	}
}

func TestEnsureRuntimeLayoutAndPathPrefixes(t *testing.T) {
	root := t.TempDir()
	rt := filepath.Join(root, "runtime")
	if err := EnsureRuntimeLayout(rt); err != nil {
		t.Fatalf("EnsureRuntimeLayout: %v", err)
	}
	for _, sub := range []string{"bin", "node", "python", "uv", "cache", "state"} {
		st, err := os.Stat(filepath.Join(rt, sub))
		if err != nil || !st.IsDir() {
			t.Fatalf("missing dir %s: %v", sub, err)
		}
	}
	readme := filepath.Join(rt, RuntimeReadmeName)
	if _, err := os.Stat(readme); err != nil {
		t.Fatalf("readme: %v", err)
	}
	// 再次调用不覆盖用户修改
	if err := os.WriteFile(readme, []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRuntimeLayout(rt); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(readme)
	if string(b) != "user" {
		t.Fatalf("readme should not be overwritten, got %q", b)
	}

	// 仅 bin 存在有效工具路径前缀时
	prefs := PathPrefixes(rt)
	if len(prefs) < 1 || prefs[0] != filepath.Join(rt, "bin") {
		t.Fatalf("prefixes=%v", prefs)
	}
	// 创建 node/bin 后应进入列表
	if err := os.MkdirAll(filepath.Join(rt, "node", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	prefs = PathPrefixes(rt)
	if len(prefs) < 2 {
		t.Fatalf("expected bin+node/bin, got %v", prefs)
	}
}

func TestPrependPathIdempotent(t *testing.T) {
	t.Parallel()
	sep := string(os.PathListSeparator)
	a := filepath.Join("rt", "bin")
	b := filepath.Join("rt", "node", "bin")
	got := PrependPath(" /usr/bin ", []string{a, b})
	if !strings.HasPrefix(got, a+sep+b+sep) && !strings.HasPrefix(got, a+sep+b) {
		// 允许末尾无多余 sep
		if got != a+sep+b+sep+"/usr/bin" && got != a+sep+b+sep+" /usr/bin " {
			// normalize: PrependPath trims parts only on split of current, not current as whole
			if !strings.Contains(got, a) || !strings.Contains(got, "/usr/bin") {
				t.Fatalf("got %q", got)
			}
		}
	}
	again := PrependPath(got, []string{a, b})
	if strings.Count(again, a) != 1 {
		t.Fatalf("not idempotent: %q", again)
	}
}

func TestLookPathWithPrefixesFindsFile(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "toolprobe"
	path := filepath.Join(bin, name)
	if runtime.GOOS == "windows" {
		path = path + ".exe"
		name = "toolprobe"
	}
	if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 无系统 PATH 依赖：自定义 lookPath 失败，前缀仍能命中
	got, err := LookPathWithPrefixes(name, []string{bin}, func(string) (string, error) {
		return "", os.ErrNotExist
	})
	if err != nil {
		t.Fatalf("LookPathWithPrefixes: %v", err)
	}
	if filepath.Base(got) != filepath.Base(path) {
		t.Fatalf("got %q want base of %q", got, path)
	}
}

func TestResolveCommandWithPrefixesMissing(t *testing.T) {
	_, err := ResolveCommandWithPrefixes("definitely-not-a-binary-xyz", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "未找到可执行文件") {
		t.Fatalf("msg=%v", err)
	}
}
