package runtime

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNpmCommandsUseIndependentPrefixAndPersistentCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-bin 测试仅支持 Linux/macOS")
	}
	runtimeDir := t.TempDir()
	if err := EnsureRuntimeLayout(runtimeDir); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(runtimeDir, "npm-args")
	envFile := filepath.Join(runtimeDir, "npm-env")
	npmBody := `printf '%s\n' "$@" > '` + argsFile + `'
printf '%s\n' "$npm_config_cache|$NPM_CONFIG_CACHE|$UV_CACHE_DIR|$XDG_CACHE_HOME" > '` + envFile + `'
echo "added 1 package"
`
	makeFakeBin(t, filepath.Join(runtimeDir, RuntimeSubdirBin), "npm", npmBody)

	dm := NewDependencyManager(runtimeDir, nil)
	if _, err := dm.InstallDep(context.Background(), DepKindNpm, "lodash@4.17.21"); err != nil {
		t.Fatalf("InstallDep: %v", err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(runtimeDir, RuntimeSubdirNpm)
	if got, want := string(args), "install\n--prefix\n"+prefix+"\nlodash@4.17.21\n"; got != want {
		t.Fatalf("npm args=%q, want %q", got, want)
	}
	env, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(runtimeDir, RuntimeSubdirCache)
	wantEnv := strings.Join([]string{
		filepath.Join(cacheDir, "npm"),
		filepath.Join(cacheDir, "npm"),
		filepath.Join(cacheDir, "uv"),
		cacheDir,
	}, "|") + "\n"
	if got := string(env); got != wantEnv {
		t.Fatalf("npm cache env=%q, want %q", got, wantEnv)
	}
}

// pip 依赖区不再使用 venv：uv 必须以 --target 指向 runtime/pip，且带 --python
// 固定解析用解释器，否则 uv 会自行挑版本或尝试下载。
func TestPipCommandsTargetVolumeDirectoryWithoutVenv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-bin 测试仅支持 Linux/macOS")
	}
	runtimeDir := t.TempDir()
	if err := EnsureRuntimeLayout(runtimeDir); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(runtimeDir, RuntimeSubdirBin)
	makeFakeBin(t, bin, "python3", "exit 0")
	argsFile := filepath.Join(runtimeDir, "uv-args")
	makeFakeBin(t, bin, "uv", `printf '%s\n' "$@" > '`+argsFile+`'
echo '[]'`)

	dm := NewDependencyManager(runtimeDir, nil)
	if _, err := dm.InstallDep(context.Background(), DepKindPip, "httpx@0.27.0"); err != nil {
		t.Fatalf("InstallDep: %v", err)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(raw)), "\n")
	want := []string{
		"pip", "install",
		"--target", filepath.Join(runtimeDir, RuntimeSubdirPip),
		"--no-python-downloads",
		"--upgrade", "httpx==0.27.0",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("uv args=%v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, RuntimeSubdirPip)); err != nil {
		t.Fatalf("pip target dir should be created: %v", err)
	}
	// venv 已彻底移除，不应再出现任何 .venv 残留。
	if _, err := os.Stat(filepath.Join(runtimeDir, RuntimeSubdirPip, ".venv")); !os.IsNotExist(err) {
		t.Fatalf("pip area must not contain a venv: %v", err)
	}
}

// --target 目录里没有 venv 引导包，列表不应再过滤 pip/setuptools/wheel：
// 它们出现时就是用户显式安装的结果。
func TestListPipShowsEveryPackageInTargetDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-bin 测试仅支持 Linux/macOS")
	}
	runtimeDir := t.TempDir()
	if err := EnsureRuntimeLayout(runtimeDir); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(runtimeDir, RuntimeSubdirBin)
	makeFakeBin(t, bin, "python3", "exit 0")
	uvBody := `echo '[{"name":"pip","version":"24.0"},{"name":"requests","version":"2.32.3"},{"name":"urllib3","version":"2.2.2"}]'`
	makeFakeBin(t, bin, "uv", uvBody)

	dm := NewDependencyManager(runtimeDir, nil)
	result, err := dm.ListDeps(context.Background(), DepKindPip)
	if err != nil {
		t.Fatalf("ListDeps: %v", err)
	}
	if !result.Ready || result.Count != 3 {
		t.Fatalf("result=%+v", result)
	}
	versions := make(map[string]string, len(result.Items))
	for _, item := range result.Items {
		versions[item.Name] = item.Version
	}
	if versions["pip"] != "24.0" || versions["requests"] != "2.32.3" || versions["urllib3"] != "2.2.2" {
		t.Fatalf("packages were hidden: %+v", result.Items)
	}
}
