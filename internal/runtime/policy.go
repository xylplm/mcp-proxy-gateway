// Package runtime 提供 stdio 本地运行时的安全策略、命令校验、环境清理、
// 卷路径解析、npm/pip 共享依赖管理与进程级加固。
//
// 解释器（Node / Python / uv）由镜像内置，本包不做运行期下载安装。
//
// 边界：
//   - 不提供任意 shell / 任意 URL 装包；
//   - 策略对 stdio 热生效，不影响远程传输主路径；
//   - 默认兼容模板常用命令（node/npx/uvx 等）；不含 docker，容器内无法执行。
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
	// DefaultStdioSecurityMode 为上游未声明 securityProfile.mode 时的默认档位。
	DefaultStdioSecurityMode StdioSecurityMode
	// StrictCommandAllowlist 为严格档与全局 allowlist 的交集候选；空则使用 DefaultStrictCommandAllowlist。
	StrictCommandAllowlist []string
	// StrictPackageAllowlist 为严格档允许 npx/uvx 执行的包/工具名（支持 @scope/*）；空则使用 DefaultStrictPackageAllowlist。
	StrictPackageAllowlist []string
	// GlobalFileRoots 为严格/声明文件策略时的全局默认允许根。
	GlobalFileRoots []string
	// BrowseExtraRoots 为管理台路径选择器额外可浏览根（不参与 stdio 文件策略校验）。
	BrowseExtraRoots []string
	// StrictPathOnlyRuntime 为 true 时严格档只接受受信运行时目录内的可执行文件：
	// 运行时卷前缀 ∪ 镜像内置解释器目录；系统 PATH 上的其他位置一律拒绝。
	StrictPathOnlyRuntime bool
	// StrictNetworkDefault 为严格档未声明网络策略时的默认（deny 或 allowlist）。
	StrictNetworkDefault NetworkAccessMode
	// StrictAllowPolicyOnly 为 true 时，即使无内核隔离能力也允许严格档仅策略运行（默认 false 在有强制隔离需求时可配）。
	// Phase A 无真隔离时该标志不影响连接（策略-only 为唯一路径）；预留给 Phase C。
	StrictAllowPolicyOnly bool
	// NpmRegistry / PipIndexURL / UvIndexURL 为 stdio 子进程包仓库镜像（非空时注入对应环境变量）。
	// 空表示不覆盖子进程/上游 env 的默认源。
	NpmRegistry string
	PipIndexURL string
	UvIndexURL  string
}

// DefaultCommandAllowlist 与模板市场常用 stdio 命令对齐。
//
// 不含 docker：网关运行在容器内，镜像不提供 docker CLI 也不挂载宿主 socket，
// 因此 docker 命令永远不可用；挂载 socket 又等同于把宿主 root 交给子进程。
// 确有需要的自建部署可自行在 runtime.command_allowlist 中加回。
func DefaultCommandAllowlist() []string {
	return []string{
		"node",
		"npx",
		"npm",
		"python",
		"python3",
		"uv",
		"uvx",
	}
}

// DefaultProbeTools 为管理台 Doctor 探测的逻辑工具名。
// 只探测完整镜像内置或用户可放入 runtime/bin 的工具，
// 避免出现用户无法补齐的永久缺失项。
func DefaultProbeTools() []string {
	return []string{
		"node",
		"npx",
		"npm",
		"python",
		"python3",
		"uv",
		"uvx",
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
	if p.StrictCommandAllowlist != nil {
		p.StrictCommandAllowlist = normalizeNameList(p.StrictCommandAllowlist)
	}
	if p.StrictPackageAllowlist != nil {
		p.StrictPackageAllowlist = normalizePackageAllowlist(p.StrictPackageAllowlist)
	}
	p.ExtraSensitiveEnvPrefixes = normalizePrefixList(p.ExtraSensitiveEnvPrefixes)
	p.GlobalFileRoots = normalizePathList(p.GlobalFileRoots)
	p.BrowseExtraRoots = normalizePathList(p.BrowseExtraRoots)
	p.DefaultStdioSecurityMode = NormalizeSecurityMode(string(p.DefaultStdioSecurityMode), SecurityModeStandard)
	switch p.StrictNetworkDefault {
	case NetworkAccessDeny, NetworkAccessAllowlist:
	default:
		p.StrictNetworkDefault = NetworkAccessAllowlist
	}
	return p
}

// DefaultPolicy 返回与网关出厂配置一致的策略（stdio 启用 + 默认白名单 + 进程加固）。
func DefaultPolicy() Policy {
	return NormalizePolicy(Policy{
		StdioEnabled:             true,
		CommandAllowlist:         DefaultCommandAllowlist(),
		ProcessHardening:         true,
		DefaultStdioSecurityMode: SecurityModeStandard,
		StrictCommandAllowlist:   DefaultStrictCommandAllowlist(),
		StrictPackageAllowlist:   DefaultStrictPackageAllowlist(),
		StrictPathOnlyRuntime:    true,
		StrictNetworkDefault:     NetworkAccessAllowlist,
		StrictAllowPolicyOnly:    true, // Phase A：仅策略运行；有 bwrap 后再收紧默认
	})
}

// CommandBaseName 提取命令基名（去掉路径与 Windows 扩展名），小写。
//
// 同时识别 `/` 与 `\` 分隔符，确保 Linux 上处理 Windows 风格配置路径时
// 仍能得到正确基名（如 `C:\Tools\uvx.exe` → `uvx`）。
func CommandBaseName(command string) string {
	raw := strings.TrimSpace(command)
	if raw == "" {
		return ""
	}
	// filepath.Base 仅按本机 Separator 切分；stdio 命令可能来自跨平台配置。
	raw = strings.ReplaceAll(raw, "\\", "/")
	base := filepath.Base(raw)
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == "/" {
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
