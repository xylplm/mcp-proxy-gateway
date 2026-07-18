// Package runtime 提供 stdio 本地运行时的安全策略、命令校验、环境清理、
// 卷路径解析、受控预置安装与进程级加固。
//
// 边界：
//   - 不提供任意 shell / 任意 URL 装包；
//   - 策略对 stdio 热生效，不影响远程传输主路径；
//   - 默认兼容模板常用命令（node/npx/uvx 等）；docker 仍在默认白名单以兼容内置模板。
package runtime

import (
	"path/filepath"
	"strings"
)

// Policy 为 stdio 执行策略快照（由配置归一化而来）。
type Policy struct {
	// StdioEnabled 为 false 时拒绝一切 stdio 上游配置与连接。
	StdioEnabled bool
	// CommandAllowlist 为允许的可执行文件基名（小写）；空表示仅拒绝危险命令（测试/高级用法）。
	// 生产配置层会把空列表回填为 DefaultCommandAllowlist，避免误开「允许一切」。
	CommandAllowlist []string
	// ExtraSensitiveEnvPrefixes 为追加到内置敏感前缀的自定义前缀（大写比较）。
	ExtraSensitiveEnvPrefixes []string
	// ProcessHardening 为 true 时对 stdio 子进程应用平台可用的进程隔离（Linux: 进程组/父亡杀子等）。
	// 默认 true；不影响远程传输。卷内 runtime PATH 优先解析始终开启（非策略开关）。
	ProcessHardening bool
}

// DefaultCommandAllowlist 与模板市场常用 stdio 命令对齐。
func DefaultCommandAllowlist() []string {
	return []string{
		"node",
		"npx",
		"npm",
		"python",
		"python3",
		"uv",
		"uvx",
		"docker",
	}
}

// DefaultProbeTools 为管理台 Doctor 探测的逻辑工具名。
func DefaultProbeTools() []string {
	return []string{
		"node",
		"npx",
		"npm",
		"python",
		"python3",
		"uv",
		"uvx",
		"docker",
	}
}

// NormalizePolicy 清洗 allowlist / 前缀，并补齐布尔默认语义。
//
// CommandAllowlist 为 nil 或空时：保持空切片语义给 ValidateCommand（denylist-only）。
// 产品配置层（config.NormalizeYAMLConfig）负责把空列表回填为默认白名单。
func NormalizePolicy(p Policy) Policy {
	if p.CommandAllowlist != nil {
		p.CommandAllowlist = normalizeNameList(p.CommandAllowlist)
	}
	p.ExtraSensitiveEnvPrefixes = normalizePrefixList(p.ExtraSensitiveEnvPrefixes)
	return p
}

// DefaultPolicy 返回与网关出厂配置一致的策略（stdio 启用 + 默认白名单 + 进程加固）。
func DefaultPolicy() Policy {
	return NormalizePolicy(Policy{
		StdioEnabled:     true,
		CommandAllowlist: DefaultCommandAllowlist(),
		ProcessHardening: true,
	})
}

// CommandBaseName 提取命令基名（去掉路径与 Windows 扩展名），小写。
func CommandBaseName(command string) string {
	raw := strings.TrimSpace(command)
	if raw == "" {
		return ""
	}
	base := filepath.Base(raw)
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	// Windows 可执行扩展：校验时按基名匹配 allowlist。
	lower := strings.ToLower(base)
	for _, ext := range []string{".exe", ".cmd", ".bat", ".com"} {
		if strings.HasSuffix(lower, ext) {
			return strings.TrimSuffix(lower, ext)
		}
	}
	return lower
}

func normalizeNameList(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		name := CommandBaseName(item)
		if name == "" {
			// 允许配置里直接写逻辑名（无路径）。
			name = strings.ToLower(strings.TrimSpace(item))
		}
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func normalizePrefixList(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		p := strings.ToUpper(strings.TrimSpace(item))
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
