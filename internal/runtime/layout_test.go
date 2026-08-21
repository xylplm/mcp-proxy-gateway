package runtime

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
	for _, sub := range []string{"bin", "npm", "pip", "cache"} {
		st, err := os.Stat(filepath.Join(rt, sub))
		if err != nil || !st.IsDir() {
			t.Fatalf("missing dir %s: %v", sub, err)
		}
	}
	// 解释器由镜像提供，卷内不再有受管发行版与安装状态目录。
	for _, sub := range []string{"node", "python", "uv", "state"} {
		if _, err := os.Stat(filepath.Join(rt, sub)); !os.IsNotExist(err) {
			t.Fatalf("managed runtime dir %s should not exist: %v", sub, err)
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

	// 只有 bin 存在时前缀列表只含 bin（npm/pip 的可执行目录尚未生成）。
	prefs := PathPrefixes(rt)
	if len(prefs) != 1 || prefs[0] != filepath.Join(rt, RuntimeSubdirBin) {
		t.Fatalf("prefixes=%v", prefs)
	}
	// 用户手放目录必须始终排第一，其后依次是 npm CLI 与 pip 脚本。
	if err := os.MkdirAll(filepath.Join(rt, RuntimeSubdirPip, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	pathPrefixesCache.Lock()
	pathPrefixesCache.at = time.Time{}
	pathPrefixesCache.Unlock()
	prefs = PathPrefixes(rt)
	if len(prefs) != 2 || prefs[1] != filepath.Join(rt, RuntimeSubdirPip, "bin") {
		t.Fatalf("expected bin+pip/bin, got %v", prefs)
	}
	if err := os.MkdirAll(filepath.Join(rt, RuntimeSubdirNpm, "node_modules", ".bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	invalidatePathPrefixes(rt)
	prefs = PathPrefixes(rt)
	want := []string{
		filepath.Join(rt, RuntimeSubdirBin),
		filepath.Join(rt, RuntimeSubdirNpm, "node_modules", ".bin"),
		filepath.Join(rt, RuntimeSubdirPip, "bin"),
	}
	if strings.Join(prefs, "|") != strings.Join(want, "|") {
		t.Fatalf("prefix order=%v, want %v", prefs, want)
	}
}

// 严格档要求可执行文件落在运行时卷内。每个 PATH 前缀反推出的根都必须包含该前缀，
// 且不能越出运行时目录 —— 否则要么把卷内工具误判越界，要么把卷外目录放进白名单。
func TestRuntimeRootOfPrefixStaysWithinRuntimeDir(t *testing.T) {
	t.Parallel()
	for _, rt := range []string{
		filepath.Join("tmp", "runtime"),
		// 运行时目录本身叫 pip/npm：按目录名回推会错误地跳到父目录。
		filepath.Join("tmp", "pip"),
		filepath.Join("tmp", "npm"),
	} {
		want := filepath.Clean(rt)
		for _, prefix := range runtimePathCandidates(rt) {
			root := runtimeRootOfPrefix(prefix)
			if !pathInRoot(filepath.Clean(prefix), root) {
				t.Fatalf("runtimeDir=%q prefix %q not inside root %q", rt, prefix, root)
			}
			if !pathInRoot(root, want) {
				t.Fatalf("runtimeDir=%q prefix %q => root %q escapes runtime dir", rt, prefix, root)
			}
		}
	}
}

// npm 的 .bin 条目是指向 ../<pkg>/bin/*.js 的符号链接，根必须宽到 node_modules，
// 否则解析后的真实路径会被判为越界。
func TestRuntimeRootOfPrefixCoversNpmBinSymlinkTargets(t *testing.T) {
	t.Parallel()
	rt := filepath.Join("tmp", "runtime")
	prefix := filepath.Join(rt, RuntimeSubdirNpm, "node_modules", ".bin")
	root := runtimeRootOfPrefix(prefix)
	target := filepath.Join(rt, RuntimeSubdirNpm, "node_modules", "some-pkg", "bin", "cli.js")
	if !pathInRoot(target, root) {
		t.Fatalf("root %q does not cover symlink target %q", root, target)
	}
}

func TestPathPrefixesReturnsCopyFromShortCache(t *testing.T) {
	rt := filepath.Join(t.TempDir(), "runtime")
	if err := os.MkdirAll(filepath.Join(rt, RuntimeSubdirBin), 0o755); err != nil {
		t.Fatal(err)
	}
	first := PathPrefixes(rt)
	if len(first) != 1 {
		t.Fatalf("prefixes=%v", first)
	}
	first[0] = "mutated"
	second := PathPrefixes(rt)
	if second[0] != filepath.Join(rt, RuntimeSubdirBin) {
		t.Fatalf("cache returned mutable data: %v", second)
	}
}

func TestPrependPathIdempotent(t *testing.T) {
	t.Parallel()
	sep := string(os.PathListSeparator)
	a := filepath.Join("rt", "bin")
	b := filepath.Join("rt", RuntimeSubdirPip, "bin")
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

func TestLookPathWithPrefixesStatusReportsMissingExecutablePermission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 没有统一的 Unix 可执行位")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bin, "toolprobe")
	if err := os.WriteFile(path, []byte("not executable"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, warning, err := LookPathWithPrefixesStatus("toolprobe", []string{bin}, func(string) (string, error) {
		return "", os.ErrNotExist
	})
	if err != nil || got != path || warning == "" {
		t.Fatalf("got path=%q warning=%q err=%v", got, warning, err)
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
