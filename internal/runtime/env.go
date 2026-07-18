package runtime

import (
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
	"POSTGRES",
	"PG",
	"REDIS_",
	"OPENAI_",
	"ANTHROPIC_",
	"GITHUB_",
	"GH_",
	"NPM_",
	"DOCKER_",
	"KUBE",
	"K8S_",
	"AZURE_",
	"GCP_",
	"GOOGLE_APPLICATION_CREDENTIALS",
}

// BuildChildEnv 构造 stdio 子进程环境变量列表。
//
// parentEnviron 形如 os.Environ()（KEY=VAL）。
// userEnv 为上游 connParams.env（已解析占位符）；其键值始终写入，即便键名敏感
// （用户显式配置的 MCP 凭证需要放行）。
// pathPrefixes 为卷内运行时目录（可选）；在用户 env 写入前幂等前置到 PATH。
//
// 返回的切片保证：
//   - 保留 PATH / HOME 等非敏感系统变量；
//   - 剥离父进程中的 MPG_* / 云密钥等；
//   - 用户 env 覆盖同名键（含 PATH，显式配置优先）。
func BuildChildEnv(parentEnviron []string, userEnv map[string]string, policy Policy, pathPrefixes ...string) []string {
	policy = NormalizePolicy(policy)
	prefixes := append([]string{}, sensitiveEnvPrefixes...)
	prefixes = append(prefixes, policy.ExtraSensitiveEnvPrefixes...)

	merged := make(map[string]string, len(parentEnviron)+len(userEnv)+1)
	order := make([]string, 0, len(parentEnviron)+len(userEnv)+1)

	put := func(key, value string, fromParent bool) {
		if key == "" {
			return
		}
		if fromParent && isSensitiveEnvKey(key, prefixes) {
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
		cur, ok := merged["PATH"]
		if !ok {
			// Windows 偶发 Path 键名；合并时统一写 PATH。
			for k, v := range merged {
				if strings.EqualFold(k, "PATH") {
					cur = v
					ok = true
					delete(merged, k)
					// 保持 order 中键名规范为 PATH
					for i, name := range order {
						if strings.EqualFold(name, "PATH") {
							order[i] = "PATH"
						}
					}
					break
				}
			}
		}
		put("PATH", PrependPath(cur, pathPrefixes), false)
	}

	for k, v := range userEnv {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		put(key, v, false)
	}

	out := make([]string, 0, len(order))
	for _, k := range order {
		v, ok := merged[k]
		if !ok {
			continue
		}
		out = append(out, k+"="+v)
	}
	return out
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
	// 通用密钥后缀启发：避免漏掉 CUSTOM_SECRET 一类。
	if strings.Contains(upper, "PASSWORD") ||
		strings.Contains(upper, "SECRET") ||
		strings.HasSuffix(upper, "_TOKEN") ||
		strings.HasSuffix(upper, "_API_KEY") ||
		upper == "TOKEN" ||
		upper == "API_KEY" {
		// PATH 等系统变量不含这些模式；允许保留。
		return true
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
