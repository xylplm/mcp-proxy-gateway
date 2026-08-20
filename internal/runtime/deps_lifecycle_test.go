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
	makeFakeBin(t, filepath.Join(runtimeDir, RuntimeSubdirNode, "bin"), "npm", npmBody)

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

func TestListPipKeepsThirdPartyBootstrapDependenciesVisible(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-bin 测试仅支持 Linux/macOS")
	}
	runtimeDir := t.TempDir()
	if err := EnsureRuntimeLayout(runtimeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runtimeDir, RuntimeSubdirPython, ".venv", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, RuntimeSubdirPython, ".venv", "bin", "python"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	uvBody := `echo '[{"name":"pip","version":"24.0"},{"name":"setuptools","version":"70.0"},{"name":"wheel","version":"0.43"},{"name":"requests","version":"2.32.3"},{"name":"urllib3","version":"2.2.2"}]'`
	makeFakeBin(t, filepath.Join(runtimeDir, RuntimeSubdirUV, "bin"), "uv", uvBody)

	dm := NewDependencyManager(runtimeDir, nil)
	result, err := dm.ListDeps(context.Background(), DepKindPip)
	if err != nil {
		t.Fatalf("ListDeps: %v", err)
	}
	if !result.Ready || result.Count != 2 {
		t.Fatalf("result=%+v", result)
	}
	versions := make(map[string]string, len(result.Items))
	for _, item := range result.Items {
		versions[item.Name] = item.Version
	}
	if versions["requests"] != "2.32.3" || versions["urllib3"] != "2.2.2" {
		t.Fatalf("third-party packages were hidden: %+v", result.Items)
	}
}
