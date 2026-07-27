package runtime

import (
	"os"
	"path/filepath"
	"strings"
)

// 从父进程继承时剥离的精确键（大写比较）。
var sensitiveEnvExact = map[string]struct{}{
	"JWT_SECRET":            {},
	"DATABASE_URL":          {},
	"POSTGRES_PASSWORD":     {},
	"PGPASSWORD":            {},
	"REDIS_PASSWORD":        {},
	"OPENAI_API_KEY":        {},
	"ANTHROPIC_API_KEY":     {},
	"AWS_SECRET_ACCESS_KEY": {},
	"AWS_ACCESS_KEY_ID":     {},
	"AWS_SESSION_TOKEN":     {},
	"GITHUB_TOKEN":          {},
	"GH_TOKEN":              {},
	"NPM_TOKEN":             {},
	"NODE_AUTH_TOKEN":       {},
	"DOCKER_PASSWORD":       {},
	"KUBECONFIG":            {},
}

// 从父进程继承时剥离的前缀（大写比较）。
var sensitiveEnvPrefixes = []string{
	"MPG_",
	"AWS_",
	"PGPASS",
	"PGSSL",
	"REDIS_",
	"OPENAI_",
	"ANTHROPIC_",
	"GITHUB_",
	"DOCKER_",
	"K8S_",
	"AZURE_",
	"GCP_",
	"GOOGLE_APPLICATION_CREDENTIALS",
}

// 常见凭证键的精确后缀；配置路径、开关和服务连接参数不因名称中包含
// PASSWORD/SECRET 等片段而被误删。
var sensitiveEnvSuffixes = []string{
	"_PASSWORD",
	"_PASSWD",
	"_SECRET",
	"_TOKEN",
	"_API_KEY",
	"_APIKEY",
	"_ACCESS_KEY",
	"_PRIVATE_KEY",
	"_CREDENTIALS",
}

// ChildEnvOptions 控制按安全档位构造子进程环境。
type ChildEnvOptions struct {
	// Mode 影响父 env 继承收紧与危险注入键剥离；空等同 standard。
	Mode StdioSecurityMode
	// RuntimeDir 非空时注入 MPG_RUNTIME_DIR，并将 cache 相关目录指到卷内（严格档）。
	RuntimeDir string
	// FileRoots 注入协作信号 MPG_FS_ALLOWLIST（分号分隔，非内核强制）。
	FileRoots []string
	// NetworkMode / NetworkHosts 注入协作信号（非内核强制）。
	NetworkMode  NetworkAccessMode
	NetworkHosts []string
}

// BuildChildEnv 构造 stdio 子进程环境变量列表。
//
// parentEnviron 形如 os.Environ()（KEY=VAL）。
// userEnv 为上游 connParams.env（已解析占位符）；其键值始终写入，即便键名敏感
// （用户显式配置的 MCP 凭证需要放行）。
// pathPrefixes 为卷内运行时目录（可选）；在用户 env 写入前幂等前置到 PATH。
//
// 返回的切片保证：
//   - 保留 PATH / HOME 等非敏感系统变量（严格档仅白名单继承）；
//   - 剥离父进程中的 MPG_* / 云密钥等；
//   - 用户 env 覆盖同名键（含 PATH，显式配置优先；严格档会拒绝危险注入键）。
func BuildChildEnv(parentEnviron []string, userEnv map[string]string, policy Policy, pathPrefixes ...string) []string {
	return BuildChildEnvWithOptions(parentEnviron, userEnv, policy, ChildEnvOptions{}, pathPrefixes...)
}

// BuildChildEnvWithOptions 与 BuildChildEnv 相同，并应用安全档位相关收紧。
func BuildChildEnvWithOptions(
	parentEnviron []string,
	userEnv map[string]string,
	policy Policy,
	opts ChildEnvOptions,
	pathPrefixes ...string,
) []string {
	policy = NormalizePolicy(policy)
	mode := NormalizeSecurityMode(string(opts.Mode), SecurityModeStandard)
	prefixes := append([]string{}, sensitiveEnvPrefixes...)
	prefixes = append(prefixes, policy.ExtraSensitiveEnvPrefixes...)

	merged := make(map[string]string, len(parentEnviron)+len(userEnv)+8)
	order := make([]string, 0, len(parentEnviron)+len(userEnv)+8)

	put := func(key, value string, fromParent bool) {
		if key == "" {
			return
		}
		if fromParent && isSensitiveEnvKey(key, prefixes) {
			return
		}
		if fromParent && mode == SecurityModeStrict && !strictInheritedEnvKey(key) {
			return
		}
		if _, exists := merged[key]; !exists {
			order = append(order, key)
		}
		merged[key] = value
	}

	for _, entry := range parentEnviron {
		k, v, ok := splitEnvEntry(entry)
		if !ok {
			continue
		}
		put(k, v, true)
	}

	// 卷路径前置：在用户 env 覆盖之前应用，用户仍可显式改写 PATH。
	if len(pathPrefixes) > 0 {
		cur := lookupPath(merged)
		put("PATH", PrependPath(cur, pathPrefixes), false)
		// 规范化 Path → PATH
		for k := range merged {
			if k != "PATH" && strings.EqualFold(k, "PATH") {
				delete(merged, k)
			}
		}
		for i, name := range order {
			if strings.EqualFold(name, "PATH") {
				order[i] = "PATH"
			}
		}
	}

	// 严格档：把包管理缓存指到 runtime/cache，降低写家目录与意外自装残留。
	if mode == SecurityModeStrict && strings.TrimSpace(opts.RuntimeDir) != "" {
		cacheDir := filepath.Join(opts.RuntimeDir, RuntimeSubdirCache)
		// 尽力创建，失败不阻断连接（权限/只读卷时仍注入路径，由子进程自行处理）。
		_ = os.MkdirAll(filepath.Join(cacheDir, "npm"), 0o755)
		_ = os.MkdirAll(filepath.Join(cacheDir, "uv"), 0o755)
		put("npm_config_cache", filepath.Join(cacheDir, "npm"), false)
		put("NPM_CONFIG_CACHE", filepath.Join(cacheDir, "npm"), false)
		put("UV_CACHE_DIR", filepath.Join(cacheDir, "uv"), false)
		put("XDG_CACHE_HOME", cacheDir, false)
		put("MPG_RUNTIME_DIR", opts.RuntimeDir, false)
	}

	if len(opts.FileRoots) > 0 {
		put("MPG_FS_ALLOWLIST", strings.Join(normalizePathList(opts.FileRoots), ";"), false)
	}
	if opts.NetworkMode != "" && opts.NetworkMode != NetworkAccessInherit {
		put("MPG_NETWORK_MODE", string(opts.NetworkMode), false)
	}
	if len(opts.NetworkHosts) > 0 {
		put("MPG_NETWORK_ALLOWLIST", strings.Join(normalizeHostList(opts.NetworkHosts), ","), false)
	}
	put("MPG_STDIO_SECURITY_MODE", string(mode), false)

	for k, v := range userEnv {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		if mode == SecurityModeStrict && isDangerousUserEnvKey(key) {
			continue
		}
		// 严格档：禁止用户把 PATH 指到完全架空 runtime 前缀之外的场景仍允许覆盖，
		// 但会在 PATH 上再次前置 pathPrefixes。
		put(key, v, false)
	}

	if mode == SecurityModeStrict && len(pathPrefixes) > 0 {
		cur := lookupPath(merged)
		put("PATH", PrependPath(cur, pathPrefixes), false)
	}

	out := make([]string, 0, len(order))
	seenOut := map[string]struct{}{}
	for _, k := range order {
		if _, ok := seenOut[k]; ok {
			continue
		}
		v, ok := merged[k]
		if !ok {
			continue
		}
		// 去重 Path/PATH
		if strings.EqualFold(k, "PATH") && k != "PATH" {
			continue
		}
		seenOut[k] = struct{}{}
		out = append(out, k+"="+v)
	}
	// 合并后可能新增键未在 order 中
	for k, v := range merged {
		if _, ok := seenOut[k]; ok {
			continue
		}
		if strings.EqualFold(k, "PATH") && k != "PATH" {
			continue
		}
		out = append(out, k+"="+v)
	}
	return out
}

func lookupPath(merged map[string]string) string {
	if cur, ok := merged["PATH"]; ok {
		return cur
	}
	for k, v := range merged {
		if strings.EqualFold(k, "PATH") {
			return v
		}
	}
	return ""
}

// strictInheritedEnvKey 严格档允许从父进程继承的键。
func strictInheritedEnvKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	switch upper {
	case "PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "LC_CTYPE",
		"TERM", "TMPDIR", "TMP", "TEMP", "TZ", "SYSTEMROOT", "COMSPEC",
		"NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE", "OS", "HOMEDRIVE", "HOMEPATH":
		return true
	default:
		// Windows 动态环境
		if strings.HasPrefix(upper, "LC_") {
			return true
		}
		return false
	}
}

// isDangerousUserEnvKey 严格档拒绝用户注入的动态链接/预加载类键。
func isDangerousUserEnvKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	switch upper {
	case "LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH",
		"NODE_OPTIONS", "PYTHONSTARTUP", "PYTHONPATH", "PERL5OPT", "RUBYOPT",
		"BASH_ENV", "ENV", "SHELLOPTS", "PROMPT_COMMAND":
		return true
	default:
		return false
	}
}

func splitEnvEntry(entry string) (key, value string, ok bool) {
	if entry == "" {
		return "", "", false
	}
	i := strings.IndexByte(entry, '=')
	if i <= 0 {
		return "", "", false
	}
	return entry[:i], entry[i+1:], true
}

func isSensitiveEnvKey(key string, prefixes []string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if upper == "" {
		return false
	}
	if _, ok := sensitiveEnvExact[upper]; ok {
		return true
	}
	// 仅匹配完整凭证词或精确后缀，避免误删 SECRETS_DIR、NO_SECRET_SCAN
	// 以及 NPM_CONFIG_REGISTRY、PGHOST 等业务配置。
	switch upper {
	case "TOKEN", "API_KEY", "PASSWORD", "SECRET":
		return true
	}
	for _, suffix := range sensitiveEnvSuffixes {
		if strings.HasSuffix(upper, suffix) {
			return true
		}
	}
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		if strings.HasPrefix(upper, p) {
			return true
		}
	}
	return false
}
