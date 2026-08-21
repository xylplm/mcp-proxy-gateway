package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildChildEnvScrubsParentSecretsKeepsPath(t *testing.T) {
	t.Parallel()
	parent := []string{
		"PATH=/usr/bin",
		"HOME=/home/mpg",
		"MPG_PG_DSN=postgres://secret",
		"MPG_REDIS_ADDR=redis:6379",
		"OPENAI_API_KEY=sk-test",
		"MY_CUSTOM_SECRET=abc",
		"NORMAL_FLAG=1",
	}
	out := BuildChildEnv(parent, nil, Policy{StdioEnabled: true})
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "PATH=/usr/bin") {
		t.Fatalf("PATH must be preserved: %v", out)
	}
	if !strings.Contains(joined, "HOME=/home/mpg") {
		t.Fatalf("HOME must be preserved: %v", out)
	}
	if !strings.Contains(joined, "NORMAL_FLAG=1") {
		t.Fatalf("normal vars must be preserved: %v", out)
	}
	for _, bad := range []string{"MPG_PG_DSN", "MPG_REDIS_ADDR", "OPENAI_API_KEY", "MY_CUSTOM_SECRET"} {
		if strings.Contains(joined, bad+"=") {
			t.Fatalf("sensitive %s must be scrubbed: %v", bad, out)
		}
	}
}

func TestBuildChildEnvKeepsRuntimeConfigurationVariables(t *testing.T) {
	t.Parallel()
	parent := []string{
		"PATH=/usr/bin",
		"NPM_CONFIG_REGISTRY=https://registry.example.test/npm/",
		"PGHOST=db.internal",
		"PGPORT=5432",
		"POSTGRES_HOST=db.internal",
		"SECRETS_DIR=/var/lib/mcp/secrets",
		"NO_SECRET_SCAN=1",
	}
	out := BuildChildEnv(parent, nil, Policy{StdioEnabled: true})
	joined := strings.Join(out, "\n")
	for _, want := range []string{
		"NPM_CONFIG_REGISTRY=",
		"PGHOST=",
		"PGPORT=",
		"POSTGRES_HOST=",
		"SECRETS_DIR=",
		"NO_SECRET_SCAN=",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("parent configuration %s must be preserved: %v", want, out)
		}
	}
}

func TestBuildChildEnvUserOverridesAndExplicitSecretsAllowed(t *testing.T) {
	t.Parallel()
	parent := []string{
		"PATH=/usr/bin",
		"TOKEN=parent-token",
		"API_KEY=parent",
	}
	user := map[string]string{
		"TOKEN":   "mcp-user-token",
		"API_KEY": "from-upstream",
		"FOO":     "bar",
	}
	out := BuildChildEnv(parent, user, Policy{StdioEnabled: true})
	env := map[string]string{}
	for _, e := range out {
		k, v, ok := splitEnvEntry(e)
		if ok {
			env[k] = v
		}
	}
	if env["PATH"] != "/usr/bin" {
		t.Fatalf("PATH=%q", env["PATH"])
	}
	if env["TOKEN"] != "mcp-user-token" {
		t.Fatalf("user TOKEN should override/be kept, got %q", env["TOKEN"])
	}
	if env["API_KEY"] != "from-upstream" {
		t.Fatalf("user API_KEY should be kept, got %q", env["API_KEY"])
	}
	if env["FOO"] != "bar" {
		t.Fatalf("FOO=%q", env["FOO"])
	}
}

func TestBuildChildEnvStrictModeMinimalInherit(t *testing.T) {
	t.Parallel()
	parent := []string{
		"PATH=/usr/bin",
		"HOME=/home/mpg",
		"RANDOM_APP_FLAG=1",
		"LD_PRELOAD=/tmp/x.so",
	}
	user := map[string]string{
		"LD_PRELOAD":   "/evil.so",
		"NODE_OPTIONS": "--require ./x",
		"MCP_TOKEN":    "ok",
	}
	out := BuildChildEnvWithOptions(parent, user, DefaultPolicy(), ChildEnvOptions{
		Mode:       SecurityModeStrict,
		RuntimeDir: "/data/runtime",
	}, "/data/runtime/bin")
	env := map[string]string{}
	for _, e := range out {
		k, v, ok := splitEnvEntry(e)
		if ok {
			env[k] = v
		}
	}
	if env["HOME"] != "/home/mpg" {
		t.Fatalf("HOME should inherit: %v", env["HOME"])
	}
	if _, ok := env["RANDOM_APP_FLAG"]; ok {
		t.Fatalf("strict must not inherit random parent env: %v", out)
	}
	if _, ok := env["LD_PRELOAD"]; ok {
		t.Fatalf("dangerous user env must be dropped: %v", out)
	}
	if _, ok := env["NODE_OPTIONS"]; ok {
		t.Fatalf("NODE_OPTIONS must be dropped: %v", out)
	}
	if env["MCP_TOKEN"] != "ok" {
		t.Fatalf("benign user env must remain: %v", env["MCP_TOKEN"])
	}
	if env["MPG_STDIO_SECURITY_MODE"] != "strict" {
		t.Fatalf("mode signal missing: %v", env["MPG_STDIO_SECURITY_MODE"])
	}
	if !strings.Contains(env["PATH"], "/data/runtime/bin") {
		t.Fatalf("runtime path prefix missing: %q", env["PATH"])
	}
}

func TestBuildChildEnvExtraPrefix(t *testing.T) {
	t.Parallel()
	parent := []string{"PATH=/bin", "CORP_INTERNAL_KEY=1", "OK=1"}
	out := BuildChildEnv(parent, nil, Policy{
		StdioEnabled:              true,
		ExtraSensitiveEnvPrefixes: []string{"CORP_"},
	})
	joined := strings.Join(out, "\n")
	if strings.Contains(joined, "CORP_INTERNAL_KEY=") {
		t.Fatalf("extra prefix should scrub: %v", out)
	}
	if !strings.Contains(joined, "OK=1") {
		t.Fatalf("OK should remain: %v", out)
	}
}

func envMapFrom(t *testing.T, out []string) map[string]string {
	t.Helper()
	env := map[string]string{}
	for _, e := range out {
		k, v, ok := splitEnvEntry(e)
		if ok {
			env[k] = v
		}
	}
	return env
}

// TestBuildChildEnvPackageMirrorsInjected 验证镜像非空时注入对应环境变量（大小写双写）。
func TestBuildChildEnvPackageMirrorsInjected(t *testing.T) {
	t.Parallel()
	policy := Policy{
		StdioEnabled: true,
		NpmRegistry:  "https://registry.npmmirror.com",
		PipIndexURL:  "https://pypi.tuna.tsinghua.edu.cn/simple",
		UvIndexURL:   "https://pypi.tuna.tsinghua.edu.cn/simple",
	}
	out := BuildChildEnv([]string{"PATH=/usr/bin"}, nil, policy)
	env := envMapFrom(t, out)
	if env["NPM_CONFIG_REGISTRY"] != policy.NpmRegistry {
		t.Fatalf("NPM_CONFIG_REGISTRY=%q", env["NPM_CONFIG_REGISTRY"])
	}
	if env["npm_config_registry"] != policy.NpmRegistry {
		t.Fatalf("npm_config_registry=%q", env["npm_config_registry"])
	}
	if env["PIP_INDEX_URL"] != policy.PipIndexURL {
		t.Fatalf("PIP_INDEX_URL=%q", env["PIP_INDEX_URL"])
	}
	// uv 新旧变量名都注入，兼容不同版本。
	if env["UV_DEFAULT_INDEX"] != policy.UvIndexURL {
		t.Fatalf("UV_DEFAULT_INDEX=%q", env["UV_DEFAULT_INDEX"])
	}
	if env["UV_INDEX_URL"] != policy.UvIndexURL {
		t.Fatalf("UV_INDEX_URL=%q", env["UV_INDEX_URL"])
	}
}

// TestBuildChildEnvPackageMirrorsEmptyNotInjected 验证镜像为空时不注入。
func TestBuildChildEnvPackageMirrorsEmptyNotInjected(t *testing.T) {
	t.Parallel()
	out := BuildChildEnv([]string{"PATH=/usr/bin"}, nil, Policy{StdioEnabled: true})
	env := envMapFrom(t, out)
	for _, key := range []string{"NPM_CONFIG_REGISTRY", "npm_config_registry", "PIP_INDEX_URL", "UV_DEFAULT_INDEX", "UV_INDEX_URL"} {
		if _, ok := env[key]; ok {
			t.Fatalf("空镜像不应注入 %s", key)
		}
	}
}

// TestBuildChildEnvUserEnvOverridesMirror 验证上游 env 显式配置优先于镜像默认。
func TestBuildChildEnvUserEnvOverridesMirror(t *testing.T) {
	t.Parallel()
	policy := Policy{
		StdioEnabled: true,
		NpmRegistry:  "https://registry.npmmirror.com",
	}
	user := map[string]string{
		"NPM_CONFIG_REGISTRY": "https://my-private.registry.local",
	}
	out := BuildChildEnv([]string{"PATH=/usr/bin"}, user, policy)
	env := envMapFrom(t, out)
	if env["NPM_CONFIG_REGISTRY"] != "https://my-private.registry.local" {
		t.Fatalf("用户 env 应覆盖镜像默认，got=%q", env["NPM_CONFIG_REGISTRY"])
	}
}

func TestBuildChildEnvPrependsRuntimePath(t *testing.T) {
	t.Parallel()
	parent := []string{"PATH=/usr/bin", "MPG_PG_DSN=secret"}
	rtBin := "/data/runtime/bin"
	out := BuildChildEnv(parent, nil, Policy{StdioEnabled: true}, rtBin)
	env := map[string]string{}
	for _, e := range out {
		k, v, ok := splitEnvEntry(e)
		if ok {
			env[k] = v
		}
	}
	if !strings.HasPrefix(env["PATH"], rtBin) {
		t.Fatalf("PATH should start with runtime bin, got %q", env["PATH"])
	}
	if strings.Contains(strings.Join(out, "\n"), "MPG_PG_DSN=") {
		t.Fatalf("secrets must stay scrubbed: %v", out)
	}
	// 用户显式 PATH 覆盖
	out2 := BuildChildEnv(parent, map[string]string{"PATH": "/only"}, Policy{StdioEnabled: true}, rtBin)
	env2 := map[string]string{}
	for _, e := range out2 {
		k, v, ok := splitEnvEntry(e)
		if ok {
			env2[k] = v
		}
	}
	if env2["PATH"] != "/only" {
		t.Fatalf("user PATH should win, got %q", env2["PATH"])
	}
}

func TestBuildChildEnvRuntimeVariablesAndModuleLookup(t *testing.T) {
	t.Parallel()
	runtimeDir := filepath.Join("/data", "runtime")
	parent := []string{
		"PATH=/usr/bin",
		"NODE_PATH=/parent/modules",
	}
	user := map[string]string{"NODE_PATH": "/upstream/modules"}
	out := BuildChildEnvWithOptions(parent, user, DefaultPolicy(), ChildEnvOptions{
		Mode:       SecurityModeStandard,
		RuntimeDir: runtimeDir,
	}, filepath.Join(runtimeDir, RuntimeSubdirBin))
	env := envMapFrom(t, out)
	managedModules := filepath.Join(runtimeDir, RuntimeSubdirNpm, "node_modules")
	if env["MPG_RUNTIME_DIR"] != runtimeDir {
		t.Fatalf("MPG_RUNTIME_DIR=%q", env["MPG_RUNTIME_DIR"])
	}
	if !strings.HasPrefix(env["NODE_PATH"], managedModules+string(os.PathListSeparator)) {
		t.Fatalf("managed module path must be first, NODE_PATH=%q", env["NODE_PATH"])
	}
	if !strings.Contains(env["NODE_PATH"], "/upstream/modules") {
		t.Fatalf("standard mode should retain explicit upstream module lookup: %q", env["NODE_PATH"])
	}
	// pip 依赖区不用 venv，只有 PYTHONPATH 指向 runtime/pip 才能被镜像自带解释器 import。
	if env["PYTHONPATH"] != PipTargetDir(runtimeDir) {
		t.Fatalf("PYTHONPATH=%q, want %q", env["PYTHONPATH"], PipTargetDir(runtimeDir))
	}
}

func TestBuildChildEnvKeepsUpstreamPythonPathAfterVolumeDeps(t *testing.T) {
	t.Parallel()
	runtimeDir := filepath.Join("/data", "runtime")
	out := BuildChildEnvWithOptions(
		[]string{"PATH=/usr/bin", "PYTHONPATH=/parent/py"},
		map[string]string{"PYTHONPATH": "/upstream/py"},
		DefaultPolicy(),
		ChildEnvOptions{Mode: SecurityModeStandard, RuntimeDir: runtimeDir},
	)
	env := envMapFrom(t, out)
	want := PipTargetDir(runtimeDir) + string(os.PathListSeparator) + "/upstream/py"
	if env["PYTHONPATH"] != want {
		t.Fatalf("PYTHONPATH=%q, want %q", env["PYTHONPATH"], want)
	}
}

func TestBuildChildEnvStrictModeResetsNodePathAndAddsCaches(t *testing.T) {
	t.Parallel()
	runtimeDir := filepath.Join("/data", "runtime")
	out := BuildChildEnvWithOptions(
		[]string{"PATH=/usr/bin", "NODE_PATH=/parent/modules"},
		map[string]string{"NODE_PATH": "/upstream/modules"},
		DefaultPolicy(),
		ChildEnvOptions{
			Mode:                 SecurityModeStrict,
			RuntimeDir:           runtimeDir,
			PackageManagerCaches: true,
		},
	)
	env := envMapFrom(t, out)
	cacheDir := filepath.Join(runtimeDir, RuntimeSubdirCache)
	managedModules := filepath.Join(runtimeDir, RuntimeSubdirNpm, "node_modules")
	if env["MPG_RUNTIME_DIR"] != runtimeDir {
		t.Fatalf("MPG_RUNTIME_DIR=%q", env["MPG_RUNTIME_DIR"])
	}
	if env["NODE_PATH"] != managedModules {
		t.Fatalf("strict NODE_PATH=%q, want %q", env["NODE_PATH"], managedModules)
	}
	// 严格档只保留卷内依赖根，不接受父进程或上游声明的额外 Python 模块搜索路径。
	if env["PYTHONPATH"] != PipTargetDir(runtimeDir) {
		t.Fatalf("strict PYTHONPATH=%q, want %q", env["PYTHONPATH"], PipTargetDir(runtimeDir))
	}
	for key, want := range map[string]string{
		"npm_config_cache": filepath.Join(cacheDir, "npm"),
		"NPM_CONFIG_CACHE": filepath.Join(cacheDir, "npm"),
		"UV_CACHE_DIR":     filepath.Join(cacheDir, "uv"),
		"XDG_CACHE_HOME":   cacheDir,
	} {
		if env[key] != want {
			t.Fatalf("%s=%q, want %q", key, env[key], want)
		}
	}
}
