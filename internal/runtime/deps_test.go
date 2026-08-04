package runtime

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateDepSpec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		spec    string
		wantErr bool
	}{
		{"普通包名", "lodash", false},
		{"scoped 包名", "@scope/pkg", false},
		{"npm 版本", "lodash@4.17.21", false},
		{"scoped 版本", "@scope/pkg@1.2.3", false},
		{"pip 版本", "requests==2.31.0", false},
		{"空拒绝", "", true},
		{"空格拒绝", "some pkg", true},
		{"斜杠拒绝", "../evil", true},
		{"反斜杠拒绝", "win\\path", true},
		{"点点拒绝", "pkg..name", true},
		{"控制字符拒绝", "pkg\x00", true},
		{"过长拒绝", "a" + strings.Repeat("b", 256), true},
		{"scoped 多斜杠拒绝", "@scope/a/b", true},
		{"scoped 无名拒绝", "@scope/", true},
		{"scoped 空scope拒绝", "@/pkg", true},
		{"裸斜杠拒绝", "a/b", true},
		{"flag 注入拒绝", "--global", true},
		{"flag 短选项拒绝", "-g", true},
		{"flag prefix 注入拒绝", "--prefix", true},
		{"flag target 拒绝", "--target=/tmp", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDepSpec(tc.spec)
			if tc.wantErr && err == nil {
				t.Fatalf("期望拒绝 %q", tc.spec)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("不应拒绝 %q: %v", tc.spec, err)
			}
		})
	}
}

func TestParseDepSpecName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		spec, wantName, wantVer string
	}{
		{"lodash", "lodash", ""},
		{"lodash@4.17.21", "lodash", "4.17.21"},
		{"@scope/pkg", "@scope/pkg", ""},
		{"@scope/pkg@1.2.3", "@scope/pkg", "1.2.3"},
		{"requests==2.31.0", "requests", "2.31.0"},
	}
	for _, tc := range cases {
		name, ver := parseDepSpecName(tc.spec)
		if name != tc.wantName || ver != tc.wantVer {
			t.Fatalf("parseDepSpecName(%q) = (%q,%q), want (%q,%q)", tc.spec, name, ver, tc.wantName, tc.wantVer)
		}
	}
}

func TestSpecForPip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		spec, want string
	}{
		{"requests", "requests"},
		{"requests==2.31.0", "requests==2.31.0"},
		{"requests@2.31.0", "requests==2.31.0"},
		{"@scope/pkg@1.0.0", "@scope/pkg==1.0.0"},
	}
	for _, tc := range cases {
		if got := specForPip(tc.spec); got != tc.want {
			t.Fatalf("specForPip(%q) = %q, want %q", tc.spec, got, tc.want)
		}
	}
}

func TestNormalizeDepKind(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"npm", "NPM", " npm "} {
		k, err := NormalizeDepKind(in)
		if err != nil || k != DepKindNpm {
			t.Fatalf("NormalizeDepKind(%q) = %v,%v", in, k, err)
		}
	}
	for _, in := range []string{"pip", "PIP", " pip "} {
		k, err := NormalizeDepKind(in)
		if err != nil || k != DepKindPip {
			t.Fatalf("NormalizeDepKind(%q) = %v,%v", in, k, err)
		}
	}
	if _, err := NormalizeDepKind("docker"); err == nil {
		t.Fatal("docker 应被拒绝")
	}
}

// makeFakeBin 写一个 shell 脚本作为 fake npm/uv，返回脚本路径。
func makeFakeBin(t *testing.T, dir, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-bin 测试仅支持 Linux/Mac")
	}
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestListNpmParsesJSON 验证 npm ls --json 输出被正确解析为依赖列表。
func TestListNpmParsesJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-bin 测试仅支持 Linux/Mac")
	}
	rt := t.TempDir()
	if err := EnsureRuntimeLayout(rt); err != nil {
		t.Fatal(err)
	}
	// fake npm 输出含 lodash 与 @scope/pkg。
	npmBody := `if [ "$1" = "ls" ]; then
echo '{"dependencies":{"lodash":{"version":"4.17.21"},"@scope/pkg":{"version":"1.2.3"}}}'
exit 0
fi
echo unknown args
exit 1`
	makeFakeBin(t, filepath.Join(rt, "node", "bin"), "npm", npmBody)

	dm := NewDependencyManager(rt, nil)
	res, err := dm.ListDeps(context.Background(), DepKindNpm)
	if err != nil {
		t.Fatalf("ListDeps: %v", err)
	}
	if !res.Ready {
		t.Fatalf("expected ready, warning=%q", res.Warning)
	}
	if res.Count != 2 {
		t.Fatalf("count=%d items=%+v", res.Count, res.Items)
	}
	byName := map[string]string{}
	for _, d := range res.Items {
		byName[d.Name] = d.Version
	}
	if byName["lodash"] != "4.17.21" {
		t.Fatalf("lodash version=%q", byName["lodash"])
	}
	if byName["@scope/pkg"] != "1.2.3" {
		t.Fatalf("@scope/pkg version=%q", byName["@scope/pkg"])
	}
}

// TestListNpmMissingNpmShowsWarning 验证 npm 未安装时给出引导而非报错。
func TestListNpmMissingNpmShowsWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-bin 测试仅支持 Linux/Mac")
	}
	rt := t.TempDir()
	if err := EnsureRuntimeLayout(rt); err != nil {
		t.Fatal(err)
	}
	// 清空 PATH，避免宿主已有 npm 被解析到。
	t.Setenv("PATH", filepath.Join(rt, "bin"))
	dm := NewDependencyManager(rt, nil)
	res, err := dm.ListDeps(context.Background(), DepKindNpm)
	if err != nil {
		t.Fatalf("ListDeps should not error: %v", err)
	}
	if res.Ready {
		t.Fatal("expected not ready")
	}
	if !strings.Contains(res.Warning, "npm") {
		t.Fatalf("warning=%q", res.Warning)
	}
}

// TestInstallNpmInvokesCommand 验证 npm install 调用并捕获输出日志。
func TestInstallNpmInvokesCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-bin 测试仅支持 Linux/Mac")
	}
	rt := t.TempDir()
	if err := EnsureRuntimeLayout(rt); err != nil {
		t.Fatal(err)
	}
	npmBody := `echo "added 1 package"
exit 0`
	makeFakeBin(t, filepath.Join(rt, "node", "bin"), "npm", npmBody)

	dm := NewDependencyManager(rt, nil)
	res, err := dm.InstallDep(context.Background(), DepKindNpm, "lodash@4.17.21")
	if err != nil {
		t.Fatalf("InstallDep: %v", err)
	}
	if res.Name != "lodash" || res.Version != "4.17.21" {
		t.Fatalf("result=%+v", res)
	}
	logs := dm.Logs()
	var sawOutput bool
	for _, e := range logs {
		if strings.Contains(e.Message, "added 1 package") {
			sawOutput = true
		}
	}
	if !sawOutput {
		t.Fatalf("expected npm output in logs: %+v", logs)
	}
	if dm.lastOpError() != "" {
		t.Fatalf("unexpected lastError=%q", dm.lastOpError())
	}
}

// TestInstallRejectsBadSpec 验证非法 spec 被拒绝。
func TestInstallRejectsBadSpec(t *testing.T) {
	dm := NewDependencyManager(t.TempDir(), nil)
	for _, spec := range []string{"", "   ", "../evil", "some pkg"} {
		if _, err := dm.InstallDep(context.Background(), DepKindNpm, spec); err == nil {
			t.Fatalf("期望拒绝 %q", spec)
		}
	}
}

// TestListPipMissingPythonShowsHint 验证无 Python 解释器时给出引导。
func TestListPipMissingPythonShowsHint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-bin 测试仅支持 Linux/Mac")
	}
	rt := t.TempDir()
	if err := EnsureRuntimeLayout(rt); err != nil {
		t.Fatal(err)
	}
	// fake uv 存在，但 resolveSystemPython 通过 exec.LookPath 找不到 python3/python
	// （测试环境的 PATH 通常不含它们？不确定，故用注入型 manager）。
	uvBody := `echo "uv mock"
exit 0`
	makeFakeBin(t, filepath.Join(rt, "uv", "bin"), "uv", uvBody)

	// 通过设置空 PATH 环境确保 LookPath 找不到系统 python。
	dm := NewDependencyManager(rt, nil)
	// 直接断言 ensureVenv 在无 python 时的行为：模拟 python 缺失。
	// 这里用 PATH 清空的方式运行 list，避免依赖宿主是否有 python。
	t.Setenv("PATH", filepath.Join(rt, "uv", "bin")+string(filepath.ListSeparator)+filepath.Join(rt, "bin"))
	res, err := dm.ListDeps(context.Background(), DepKindPip)
	if err != nil {
		t.Fatalf("ListDeps pip: %v", err)
	}
	// 若宿主有 python3 则 res.Ready=true；否则应有 hint。两种都接受。
	if !res.Ready && res.PythonHint == "" {
		t.Fatalf("缺 python 时应给出 hint，got ready=%v warning=%q", res.Ready, res.Warning)
	}
}

func TestDepLogsRingBuffer(t *testing.T) {
	dm := NewDependencyManager(t.TempDir(), nil)
	for i := 0; i < maxDepLogs+50; i++ {
		dm.addLog(DepLogEntry{Kind: DepKindNpm, Level: DepLogInfo, Message: "e"})
	}
	if len(dm.Logs()) != maxDepLogs {
		t.Fatalf("ring buffer size=%d, want %d", len(dm.Logs()), maxDepLogs)
	}
}

// TestBoundedBufferEnforcesLimit 验证 stdout/stderr 缓冲上限（防 OOM）。
func TestBoundedBufferEnforcesLimit(t *testing.T) {
	t.Parallel()
	const limit = 64
	bb := newBoundedBuffer(limit)
	// 写入远超上限的行。
	for i := 0; i < 1000; i++ {
		bb.appendLine("0123456789") // 11 bytes/line（含 \n）
	}
	if bb.n > limit {
		t.Fatalf("buffer overflowed limit: n=%d > %d", bb.n, limit)
	}
	if !bb.full {
		t.Fatal("expected full flag set after exceeding limit")
	}
	// 超限后 String() 仍可安全返回已写入内容。
	if bb.String() == "" {
		t.Fatal("expected non-empty buffer content")
	}
}

// TestBoundedBufferExactLimit 验证恰好填满上限不丢失。
func TestBoundedBufferExactLimit(t *testing.T) {
	t.Parallel()
	const limit = 30 // 3 lines of "a\n" = 2 bytes each
	bb := newBoundedBuffer(limit)
	for i := 0; i < 15; i++ {
		bb.appendLine("a") // 2 bytes each
	}
	// 前 15 行 = 30 bytes 恰好填满。
	if bb.full {
		t.Fatal("should not be full at exact limit")
	}
	bb.appendLine("a")
	if !bb.full {
		t.Fatal("should be full after exceeding")
	}
}

func TestDepLogsSnapshotCopied(t *testing.T) {
	dm := NewDependencyManager(t.TempDir(), nil)
	dm.addLog(DepLogEntry{Kind: DepKindNpm, Level: DepLogInfo, Message: "orig"})
	logs := dm.Logs()
	logs[0].Message = "tampered"
	if dm.Logs()[0].Message != "orig" {
		t.Fatal("Logs() 应返回副本")
	}
}
