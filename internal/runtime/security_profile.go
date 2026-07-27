package runtime

import (
	"fmt"
	"path/filepath"
	"strings"
)

// 连接参数中的本地安全配置键（stdio 专用）。
const ParamSecurityProfile = "securityProfile"

// StdioSecurityMode 为本地 stdio 运行安全档位。
type StdioSecurityMode string

const (
	// SecurityModeStandard 兼容模板与日常 MCP：策略层 + 进程清理。
	SecurityModeStandard StdioSecurityMode = "standard"
	// SecurityModeStrict 收敛命令/依赖/文件/自装包；失败偏拒绝。
	SecurityModeStrict StdioSecurityMode = "strict"
	// SecurityModeUnrestricted 最大限度兼容；仍保留 shell denylist 底线。
	SecurityModeUnrestricted StdioSecurityMode = "unrestricted"
)

// FileAccessMode 为文件访问策略模式。
type FileAccessMode string

const (
	FileAccessInherit      FileAccessMode = "inherit"
	FileAccessDeny         FileAccessMode = "deny"
	FileAccessAllowlist    FileAccessMode = "allowlist"
	FileAccessUnrestricted FileAccessMode = "unrestricted"
)

// NetworkAccessMode 为出站网络策略模式。
type NetworkAccessMode string

const (
	NetworkAccessInherit      NetworkAccessMode = "inherit"
	NetworkAccessDeny         NetworkAccessMode = "deny"
	NetworkAccessAllowlist    NetworkAccessMode = "allowlist"
	NetworkAccessUnrestricted NetworkAccessMode = "unrestricted"
)

// DependencyPolicyMode 为依赖来源策略。
type DependencyPolicyMode string

const (
	DependencyInherit      DependencyPolicyMode = "inherit"
	DependencyDeclaredOnly DependencyPolicyMode = "declared_only"
	DependencyCatalogOnly  DependencyPolicyMode = "catalog_only"
	DependencyUnrestricted DependencyPolicyMode = "unrestricted"
)

// FileAccessPolicy 描述 cwd / 工作区路径约束。
type FileAccessPolicy struct {
	Mode  FileAccessMode `json:"mode"`
	Paths []string       `json:"paths,omitempty"`
}

// NetworkPolicy 描述出站网络意图（P0 为策略声明；真隔离见能力探测）。
type NetworkPolicy struct {
	Mode  NetworkAccessMode `json:"mode"`
	Hosts []string          `json:"hosts,omitempty"`
}

// SecurityProfile 为每上游 stdio 安全配置（存于 connParams）。
type SecurityProfile struct {
	Mode             StdioSecurityMode    `json:"mode"`
	FileAccess       FileAccessPolicy     `json:"fileAccess"`
	Network          NetworkPolicy        `json:"network"`
	DependencyPolicy DependencyPolicyMode `json:"dependencyPolicy,omitempty"`
	// PackageAllowlist 为严格档追加/覆盖的 npx·uvx 包白名单（与全局合并为并集）。
	PackageAllowlist []string `json:"packageAllowlist,omitempty"`
	// AllowSelfInstall 为 nil 时随档位默认。
	AllowSelfInstall *bool  `json:"allowSelfInstall,omitempty"`
	Note             string `json:"note,omitempty"`
}

// EffectiveSecurity 为合并全局默认后的生效配置。
type EffectiveSecurity struct {
	Mode                StdioSecurityMode    `json:"mode"`
	FileAccess          FileAccessPolicy     `json:"fileAccess"`
	Network             NetworkPolicy        `json:"network"`
	DependencyPolicy    DependencyPolicyMode `json:"dependencyPolicy"`
	AllowSelfInstall    bool                 `json:"allowSelfInstall"`
	ProcessHardening    bool                 `json:"processHardening"`
	StrictPathOnly      bool                 `json:"strictPathOnlyRuntime"`
	CommandAllowlist    []string             `json:"commandAllowlist"`
	PackageAllowlist    []string             `json:"packageAllowlist,omitempty"`
	RiskLevel           string               `json:"riskLevel"` // low | medium | high | critical
	Note                string               `json:"note,omitempty"`
	RequiresAck         bool                 `json:"requiresAck"`
	PolicyOnlyIsolation bool                 `json:"policyOnlyIsolation"`
}

// NormalizeSecurityMode 清洗档位；非法回退 defaultMode（再非法则 standard）。
func NormalizeSecurityMode(mode string, defaultMode StdioSecurityMode) StdioSecurityMode {
	m := StdioSecurityMode(strings.ToLower(strings.TrimSpace(mode)))
	switch m {
	case SecurityModeStandard, SecurityModeStrict, SecurityModeUnrestricted:
		return m
	}
	switch defaultMode {
	case SecurityModeStandard, SecurityModeStrict, SecurityModeUnrestricted:
		return defaultMode
	default:
		return SecurityModeStandard
	}
}

// DefaultStrictCommandAllowlist 严格档默认命令子集（无 docker/npm）。
func DefaultStrictCommandAllowlist() []string {
	return []string{"node", "npx", "python", "python3", "uv", "uvx"}
}

// NormalizeSecurityProfile 清洗用户提交的 profile（不做全局合并）。
func NormalizeSecurityProfile(p SecurityProfile) SecurityProfile {
	// 空 mode 保留为空，表示 inherit 全局默认；勿回填 standard。
	rawMode := strings.ToLower(strings.TrimSpace(string(p.Mode)))
	switch StdioSecurityMode(rawMode) {
	case SecurityModeStandard, SecurityModeStrict, SecurityModeUnrestricted:
		p.Mode = StdioSecurityMode(rawMode)
	default:
		p.Mode = ""
	}
	p.FileAccess.Mode = FileAccessMode(strings.ToLower(strings.TrimSpace(string(p.FileAccess.Mode))))
	switch p.FileAccess.Mode {
	case FileAccessInherit, FileAccessDeny, FileAccessAllowlist, FileAccessUnrestricted, "":
	default:
		p.FileAccess.Mode = FileAccessInherit
	}
	p.FileAccess.Paths = normalizePathList(p.FileAccess.Paths)
	p.Network.Mode = NetworkAccessMode(strings.ToLower(strings.TrimSpace(string(p.Network.Mode))))
	switch p.Network.Mode {
	case NetworkAccessInherit, NetworkAccessDeny, NetworkAccessAllowlist, NetworkAccessUnrestricted, "":
	default:
		p.Network.Mode = NetworkAccessInherit
	}
	p.Network.Hosts = normalizeHostList(p.Network.Hosts)
	p.PackageAllowlist = normalizePackageAllowlist(p.PackageAllowlist)
	p.DependencyPolicy = DependencyPolicyMode(strings.ToLower(strings.TrimSpace(string(p.DependencyPolicy))))
	switch p.DependencyPolicy {
	case DependencyInherit, DependencyDeclaredOnly, DependencyCatalogOnly, DependencyUnrestricted, "":
	default:
		p.DependencyPolicy = DependencyInherit
	}
	p.Note = strings.TrimSpace(p.Note)
	if len(p.Note) > 300 {
		p.Note = p.Note[:300]
	}
	return p
}

// ParseSecurityProfile 从 connParams 值解析；nil/缺省合法。
// map 输入严格校验字段类型，避免拼写或类型错误被静默归一化为更弱默认值。
func ParseSecurityProfile(raw any) (SecurityProfile, error) {
	if raw == nil {
		return SecurityProfile{}, nil
	}
	switch v := raw.(type) {
	case SecurityProfile:
		if err := validateSecurityProfileEnums(v); err != nil {
			return SecurityProfile{}, err
		}
		return NormalizeSecurityProfile(v), nil
	case map[string]any:
		p := SecurityProfile{}
		if value, ok := v["mode"]; ok && value != nil {
			m, ok := value.(string)
			if !ok {
				return SecurityProfile{}, fmt.Errorf("securityProfile.mode 必须为字符串")
			}
			p.Mode = StdioSecurityMode(m)
		}
		if value, ok := v["note"]; ok && value != nil {
			n, ok := value.(string)
			if !ok {
				return SecurityProfile{}, fmt.Errorf("securityProfile.note 必须为字符串")
			}
			p.Note = n
		}
		if value, ok := v["dependencyPolicy"]; ok && value != nil {
			dep, ok := value.(string)
			if !ok {
				return SecurityProfile{}, fmt.Errorf("securityProfile.dependencyPolicy 必须为字符串")
			}
			p.DependencyPolicy = DependencyPolicyMode(dep)
		}
		if value, ok := v["allowSelfInstall"]; ok && value != nil {
			asi, ok := value.(bool)
			if !ok {
				return SecurityProfile{}, fmt.Errorf("securityProfile.allowSelfInstall 必须为布尔值")
			}
			p.AllowSelfInstall = &asi
		}
		if value, ok := v["packageAllowlist"]; ok && value != nil {
			items, err := parseStrictStringSlice(value, "securityProfile.packageAllowlist")
			if err != nil {
				return SecurityProfile{}, err
			}
			p.PackageAllowlist = items
		}
		if value, ok := v["fileAccess"]; ok && value != nil {
			fa, ok := value.(map[string]any)
			if !ok {
				return SecurityProfile{}, fmt.Errorf("securityProfile.fileAccess 必须为对象")
			}
			if mode, ok := fa["mode"]; ok && mode != nil {
				m, ok := mode.(string)
				if !ok {
					return SecurityProfile{}, fmt.Errorf("securityProfile.fileAccess.mode 必须为字符串")
				}
				p.FileAccess.Mode = FileAccessMode(m)
			}
			if paths, ok := fa["paths"]; ok && paths != nil {
				items, err := parseStrictStringSlice(paths, "securityProfile.fileAccess.paths")
				if err != nil {
					return SecurityProfile{}, err
				}
				p.FileAccess.Paths = items
			}
		}
		if value, ok := v["network"]; ok && value != nil {
			network, ok := value.(map[string]any)
			if !ok {
				return SecurityProfile{}, fmt.Errorf("securityProfile.network 必须为对象")
			}
			if mode, ok := network["mode"]; ok && mode != nil {
				m, ok := mode.(string)
				if !ok {
					return SecurityProfile{}, fmt.Errorf("securityProfile.network.mode 必须为字符串")
				}
				p.Network.Mode = NetworkAccessMode(m)
			}
			if hosts, ok := network["hosts"]; ok && hosts != nil {
				items, err := parseStrictStringSlice(hosts, "securityProfile.network.hosts")
				if err != nil {
					return SecurityProfile{}, err
				}
				p.Network.Hosts = items
			}
		}
		if err := validateSecurityProfileEnums(p); err != nil {
			return SecurityProfile{}, err
		}
		return NormalizeSecurityProfile(p), nil
	default:
		return SecurityProfile{}, fmt.Errorf("securityProfile 必须为对象")
	}
}

func validateSecurityProfileEnums(p SecurityProfile) error {
	mode := StdioSecurityMode(strings.ToLower(strings.TrimSpace(string(p.Mode))))
	if mode != "" && mode != SecurityModeStandard && mode != SecurityModeStrict && mode != SecurityModeUnrestricted {
		return fmt.Errorf("securityProfile.mode 仅支持 standard、strict、unrestricted")
	}
	fileMode := FileAccessMode(strings.ToLower(strings.TrimSpace(string(p.FileAccess.Mode))))
	if fileMode != "" && fileMode != FileAccessInherit && fileMode != FileAccessDeny && fileMode != FileAccessAllowlist && fileMode != FileAccessUnrestricted {
		return fmt.Errorf("securityProfile.fileAccess.mode 非法")
	}
	networkMode := NetworkAccessMode(strings.ToLower(strings.TrimSpace(string(p.Network.Mode))))
	if networkMode != "" && networkMode != NetworkAccessInherit && networkMode != NetworkAccessDeny && networkMode != NetworkAccessAllowlist && networkMode != NetworkAccessUnrestricted {
		return fmt.Errorf("securityProfile.network.mode 非法")
	}
	dep := DependencyPolicyMode(strings.ToLower(strings.TrimSpace(string(p.DependencyPolicy))))
	if dep != "" && dep != DependencyInherit && dep != DependencyDeclaredOnly && dep != DependencyCatalogOnly && dep != DependencyUnrestricted {
		return fmt.Errorf("securityProfile.dependencyPolicy 非法")
	}
	return nil
}

func parseStrictStringSlice(raw any, field string) ([]string, error) {
	switch v := raw.(type) {
	case []string:
		return append([]string{}, v...), nil
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s[%d] 必须为字符串", field, i)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s 必须为字符串数组", field)
	}
}

// ValidateSecurityProfile 校验结构（不探测宿主、不合并全局）。
func ValidateSecurityProfile(raw any) (SecurityProfile, error) {
	if raw == nil {
		return SecurityProfile{}, nil
	}
	p, err := ParseSecurityProfile(raw)
	if err != nil {
		return SecurityProfile{}, err
	}
	if p.Mode != "" {
		switch p.Mode {
		case SecurityModeStandard, SecurityModeStrict, SecurityModeUnrestricted:
		default:
			return SecurityProfile{}, fmt.Errorf("securityProfile.mode 仅支持 standard、strict、unrestricted")
		}
	}
	if len(p.FileAccess.Paths) > 32 {
		return SecurityProfile{}, fmt.Errorf("文件允许路径最多 32 项")
	}
	for _, path := range p.FileAccess.Paths {
		if err := validateDeclaredPath(path); err != nil {
			return SecurityProfile{}, err
		}
	}
	if len(p.Network.Hosts) > 64 {
		return SecurityProfile{}, fmt.Errorf("网络允许主机最多 64 项")
	}
	if len(p.PackageAllowlist) > 128 {
		return SecurityProfile{}, fmt.Errorf("包白名单最多 128 项")
	}
	for _, pkg := range p.PackageAllowlist {
		if strings.HasSuffix(pkg, "/*") {
			base := strings.TrimSuffix(pkg, "/*")
			if base == "" || strings.Contains(base, "://") || strings.ContainsAny(base, " \t") {
				return SecurityProfile{}, fmt.Errorf("非法包白名单项 %q", pkg)
			}
			continue
		}
		if err := rejectDangerousPackageSpec(pkg); err != nil {
			return SecurityProfile{}, fmt.Errorf("包白名单项无效：%v", err)
		}
	}
	return p, nil
}

// ResolveEffectiveSecurity 合并全局 Policy 与上游 profile。
// cwd 仅保留兼容旧调用方；路径门禁请使用 ValidateCwdAgainstFileAccess / ValidateEffectiveSecurity*。
func ResolveEffectiveSecurity(policy Policy, profile SecurityProfile, _ string) EffectiveSecurity {
	policy = NormalizePolicy(policy)
	profile = NormalizeSecurityProfile(profile)

	mode := profile.Mode
	if mode == "" {
		mode = NormalizeSecurityMode(string(policy.DefaultStdioSecurityMode), SecurityModeStandard)
	} else {
		mode = NormalizeSecurityMode(string(mode), SecurityModeStandard)
	}

	eff := EffectiveSecurity{
		Mode:             mode,
		Note:             profile.Note,
		StrictPathOnly:   policy.StrictPathOnlyRuntime,
		ProcessHardening: policy.ProcessHardening && DescribeSandbox().ProcessHardeningSupported,
	}

	// 文件策略
	faMode := profile.FileAccess.Mode
	if faMode == "" || faMode == FileAccessInherit {
		switch mode {
		case SecurityModeStrict:
			faMode = FileAccessAllowlist
		case SecurityModeUnrestricted:
			faMode = FileAccessUnrestricted
		default:
			if len(profile.FileAccess.Paths) > 0 || len(policy.GlobalFileRoots) > 0 {
				faMode = FileAccessAllowlist
			} else {
				faMode = FileAccessUnrestricted
			}
		}
	}
	roots := append([]string{}, profile.FileAccess.Paths...)
	if len(roots) == 0 {
		roots = append(roots, policy.GlobalFileRoots...)
	}
	roots = normalizePathList(roots)
	eff.FileAccess = FileAccessPolicy{Mode: faMode, Paths: roots}

	// 网络策略
	netMode := profile.Network.Mode
	if netMode == "" || netMode == NetworkAccessInherit {
		switch mode {
		case SecurityModeStrict:
			if policy.StrictNetworkDefault == NetworkAccessDeny {
				netMode = NetworkAccessDeny
			} else {
				netMode = NetworkAccessAllowlist
			}
		case SecurityModeUnrestricted:
			netMode = NetworkAccessUnrestricted
		default:
			netMode = NetworkAccessUnrestricted
		}
	}
	hosts := normalizeHostList(profile.Network.Hosts)
	eff.Network = NetworkPolicy{Mode: netMode, Hosts: hosts}

	// 依赖策略
	dep := profile.DependencyPolicy
	if dep == "" || dep == DependencyInherit {
		switch mode {
		case SecurityModeStrict:
			dep = DependencyDeclaredOnly
		case SecurityModeUnrestricted:
			dep = DependencyUnrestricted
		default:
			dep = DependencyDeclaredOnly
		}
	}
	eff.DependencyPolicy = dep

	// 自装包
	if profile.AllowSelfInstall != nil {
		eff.AllowSelfInstall = *profile.AllowSelfInstall
	} else {
		eff.AllowSelfInstall = mode != SecurityModeStrict
	}

	// 严格档强制进程加固；StrictPathOnly 跟随全局开关（默认 true），不静默覆盖为恒 true。
	if mode == SecurityModeStrict {
		eff.ProcessHardening = policy.ProcessHardening && DescribeSandbox().ProcessHardeningSupported
		// 保持 policy.StrictPathOnlyRuntime（已写入 eff）；仅在缺省未配置时偏安全为 true。
		if !policy.StrictPathOnlyRuntime {
			eff.StrictPathOnly = false
		} else {
			eff.StrictPathOnly = true
		}
	}
	// 仅当上游显式声明完全放行时要求确认备注；全局默认 unrestricted 不拦截存量未声明上游，避免主业务被一刀切。
	if profile.Mode == SecurityModeUnrestricted {
		eff.RequiresAck = true
	}

	eff.CommandAllowlist = EffectiveCommandAllowlist(policy, mode)
	// 包白名单：全局默认 ∪ 上游追加（严格档强制使用）
	globalPkgs := policy.StrictPackageAllowlist
	if len(globalPkgs) == 0 {
		globalPkgs = DefaultStrictPackageAllowlist()
	}
	eff.PackageAllowlist = MergePackageAllowlist(globalPkgs, profile.PackageAllowlist)

	// 有内核隔离后端时，严格档不再标记为仅策略；标准/完全放行仍是策略层。
	if mode == SecurityModeStrict && IsolationAvailable() {
		eff.PolicyOnlyIsolation = false
	} else {
		eff.PolicyOnlyIsolation = true
	}
	// 风险等级依赖真实的隔离能力，必须在 PolicyOnlyIsolation 计算后确定。
	eff.RiskLevel = riskLevelFor(mode, eff)
	return eff
}

// EffectiveCommandAllowlist 按档位计算命令白名单；nil/空表示 denylist-only。
func EffectiveCommandAllowlist(policy Policy, mode StdioSecurityMode) []string {
	policy = NormalizePolicy(policy)
	mode = NormalizeSecurityMode(string(mode), SecurityModeStandard)
	base := policy.CommandAllowlist
	if base == nil {
		base = DefaultCommandAllowlist()
	}
	switch mode {
	case SecurityModeUnrestricted:
		// denylist-only：返回空切片（非 nil），Validate 走「仅 denylist」。
		return []string{}
	case SecurityModeStrict:
		strict := policy.StrictCommandAllowlist
		if len(strict) == 0 {
			strict = DefaultStrictCommandAllowlist()
		}
		return intersectNames(base, strict)
	default:
		out := append([]string{}, base...)
		return out
	}
}

// ValidateIsolationRequirement 校验严格档是否允许在无内核隔离时仅靠策略运行。
//
// 当 Linux 检测到 bubblewrap 时视为隔离后端可用；否则依赖 StrictAllowPolicyOnly。
func ValidateIsolationRequirement(policy Policy, eff EffectiveSecurity) error {
	if eff.Mode != SecurityModeStrict {
		return nil
	}
	if IsolationAvailable() {
		return nil
	}
	if policy.StrictAllowPolicyOnly {
		return nil
	}
	if eff.FileAccess.Mode == FileAccessUnrestricted && eff.Network.Mode == NetworkAccessUnrestricted {
		return nil
	}
	return fmt.Errorf("严格安全模式要求文件/网络内核隔离，但当前宿主未检测到 bubblewrap；可改用标准档、在 Linux 容器安装 bubblewrap，或在系统设置中允许仅策略运行")
}

// ValidateCommandForSecurity 按生效安全配置校验命令。
func ValidateCommandForSecurity(command string, policy Policy, eff EffectiveSecurity) error {
	policy = NormalizePolicy(policy)
	if !policy.StdioEnabled {
		return fmt.Errorf("本地 stdio 上游已禁用（runtime.stdio_enabled=false）")
	}
	// 使用生效 allowlist 覆盖策略快照。
	p := policy
	p.CommandAllowlist = eff.CommandAllowlist
	// unrestricted：空 allowlist → denylist only
	return ValidateCommand(command, p)
}

// ValidateCwdAgainstFileAccess 校验 cwd 与文件策略。
func ValidateCwdAgainstFileAccess(cwd string, fa FileAccessPolicy, mode StdioSecurityMode) error {
	cwd = strings.TrimSpace(cwd)
	fa.Mode = FileAccessMode(strings.ToLower(strings.TrimSpace(string(fa.Mode))))

	switch fa.Mode {
	case FileAccessUnrestricted, "":
		return nil
	case FileAccessDeny:
		if cwd != "" {
			return fmt.Errorf("当前文件策略禁止设置工作目录")
		}
		return nil
	case FileAccessAllowlist:
		if mode == SecurityModeStrict && cwd == "" {
			return fmt.Errorf("严格安全模式下必须设置工作目录（cwd），且位于文件允许路径内")
		}
		if cwd == "" {
			// 标准档 allowlist 但未设 cwd：允许（不强制 chdir）
			return nil
		}
		if len(fa.Paths) == 0 {
			if mode == SecurityModeStrict {
				return fmt.Errorf("严格安全模式需要配置文件允许路径（fileAccess.paths 或全局 global_file_roots）")
			}
			return nil
		}
		ok, err := pathAllowed(cwd, fa.Paths)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("工作目录不在文件允许路径内")
		}
		return nil
	default:
		return nil
	}
}

// ValidateEffectiveSecurity 对合并后的配置做门禁校验（保存/连接前）。
// command 为启动命令基名或路径（可空）；用于降低自装包启发式误报。
func ValidateEffectiveSecurity(eff EffectiveSecurity, cwd string, args []string) error {
	return ValidateEffectiveSecurityWithCommand(eff, cwd, "", args)
}

// ValidateEffectiveSecurityWithCommand 同 ValidateEffectiveSecurity，并传入 command 以改进启发式。
func ValidateEffectiveSecurityWithCommand(eff EffectiveSecurity, cwd, command string, args []string) error {
	if err := ValidateCwdAgainstFileAccess(cwd, eff.FileAccess, eff.Mode); err != nil {
		return err
	}
	if eff.Mode == SecurityModeUnrestricted && eff.RequiresAck {
		// API/导入绕过前端勾选时：要求 note 或显式 ack 标记（profile.Note 非空视为运维确认）。
		if strings.TrimSpace(eff.Note) == "" {
			return fmt.Errorf("完全放行模式须填写确认备注（securityProfile.note），表明已了解同权限执行风险")
		}
	}
	if eff.Mode == SecurityModeStrict {
		if eff.FileAccess.Mode == FileAccessAllowlist && len(eff.FileAccess.Paths) == 0 {
			return fmt.Errorf("严格安全模式需要至少一条文件允许路径")
		}
		if !eff.AllowSelfInstall {
			if err := DetectSelfInstallIntent(command, args); err != nil {
				return err
			}
		}
		// npx/uvx：允许执行，但目标包/工具必须在白名单内。
		if err := ValidateStrictLauncherTarget(command, args, eff.PackageAllowlist); err != nil {
			return err
		}
		if eff.Network.Mode == NetworkAccessAllowlist && len(eff.Network.Hosts) == 0 && eff.AllowSelfInstall {
			return fmt.Errorf("严格模式下启用自装包时必须声明网络允许主机")
		}
	}
	return nil
}

// DetectSelfInstallIntent 启发式识别明显的包安装/全局写入意图（严格档默认拒绝）。
// 仅在 command 为包管理器，或 args 呈现「install/add + 包名」形态时拦截，避免把业务参数 -g 误杀。
func DetectSelfInstallIntent(command string, args []string) error {
	if len(args) == 0 {
		return nil
	}
	cmdBase := CommandBaseName(command)
	pm := isPackageManagerArg(cmdBase)

	for i, a := range args {
		al := strings.ToLower(strings.TrimSpace(a))
		// 全局安装：仅当命令本身是包管理器
		if pm && (al == "-g" || al == "--global") {
			return fmt.Errorf("严格安全模式禁止全局安装参数")
		}
		// install / i / add：出现在包管理器子命令位置
		if al == "install" || al == "i" || (pm && al == "add") {
			if pm || i == 0 {
				return fmt.Errorf("严格安全模式禁止脚本自装包类参数（如 install）；请使用运行环境预置安装")
			}
		}
	}
	if !pm {
		return nil
	}
	joined := strings.ToLower(strings.Join(args, " "))
	patterns := []string{
		"install ",
		" install",
		"pip install",
		"tool install",
		"yarn add",
		"--global",
	}
	for _, p := range patterns {
		if strings.Contains(joined, p) {
			return fmt.Errorf("严格安全模式禁止脚本自装包：检测到 %q 类参数", strings.TrimSpace(p))
		}
	}
	// 单独一个 install
	if joined == "install" || joined == "i" {
		return fmt.Errorf("严格安全模式禁止脚本自装包类参数（如 install）；请使用运行环境预置安装")
	}
	return nil
}

func isPackageManagerArg(cmdOrArg string) bool {
	b := CommandBaseName(cmdOrArg)
	switch b {
	case "npm", "npx", "pnpm", "yarn", "pip", "pip3", "uv", "uvx":
		return true
	default:
		return false
	}
}

// pathAllowed 判断 target 是否位于 roots 之一（Clean 后前缀匹配；尽量 EvalSymlinks）。
func pathAllowed(target string, roots []string) (bool, error) {
	cleanTarget, err := normalizeExistingPath(target)
	if err != nil {
		// 目录可能尚不存在：仍按 Clean/Abs 逻辑判断声明合法性
		cleanTarget = cleanPathDecl(target)
		if cleanTarget == "" {
			return false, fmt.Errorf("工作目录路径无效")
		}
	}
	for _, root := range roots {
		cleanRoot := cleanPathDecl(root)
		if cleanRoot == "" {
			continue
		}
		if pathInRoot(cleanTarget, cleanRoot) {
			return true, nil
		}
		// 尝试对已存在 root 做 symlink 解析
		if abs, err := normalizeExistingPath(root); err == nil {
			if pathInRoot(cleanTarget, abs) {
				return true, nil
			}
		}
	}
	return false, nil
}

func pathInRoot(target, root string) bool {
	target = strings.TrimRight(filepath.Clean(target), string(filepath.Separator))
	root = strings.TrimRight(filepath.Clean(root), string(filepath.Separator))
	if target == "" || root == "" {
		return false
	}
	if strings.EqualFold(target, root) {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(strings.ToLower(target), strings.ToLower(root+sep))
}

func cleanPathDecl(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	// 拒绝空字节与明显相对逃逸在校验阶段用 Clean；相对路径转为绝对更安全
	if strings.Contains(p, "\x00") {
		return ""
	}
	clean := filepath.Clean(p)
	if clean == "." || clean == ".." {
		return ""
	}
	if !filepath.IsAbs(clean) {
		if abs, err := filepath.Abs(clean); err == nil {
			clean = abs
		}
	}
	return clean
}

func normalizeExistingPath(p string) (string, error) {
	clean := cleanPathDecl(p)
	if clean == "" {
		return "", fmt.Errorf("路径无效")
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		// 不存在时返回 clean，由调用方决定
		return clean, err
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return resolved, nil
	}
	return abs, nil
}

func validateDeclaredPath(p string) error {
	p = strings.TrimSpace(p)
	if p == "" {
		return fmt.Errorf("文件允许路径不能为空")
	}
	if strings.Contains(p, "\x00") {
		return fmt.Errorf("文件允许路径含非法字符")
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("文件允许路径不能包含父目录穿越")
	}
	// 拒绝把文件系统根当作唯一根（过于宽松，严格档无意义）
	if clean == string(filepath.Separator) || clean == filepath.VolumeName(clean)+string(filepath.Separator) {
		return fmt.Errorf("不能将文件系统根目录作为允许路径，请收窄到具体工作区")
	}
	return nil
}

func normalizePathList(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		p := strings.TrimSpace(item)
		if p == "" {
			continue
		}
		key := strings.ToLower(filepath.Clean(p))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, p)
	}
	return out
}

func normalizeHostList(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		h := strings.ToLower(strings.TrimSpace(item))
		h = strings.TrimPrefix(h, "https://")
		h = strings.TrimPrefix(h, "http://")
		if i := strings.IndexByte(h, '/'); i >= 0 {
			h = h[:i]
		}
		if h == "" || strings.ContainsAny(h, " \t") {
			continue
		}
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	return out
}

func intersectNames(a, b []string) []string {
	if len(a) == 0 {
		return normalizeNameList(b)
	}
	if len(b) == 0 {
		return normalizeNameList(a)
	}
	set := map[string]struct{}{}
	for _, x := range normalizeNameList(b) {
		set[x] = struct{}{}
	}
	out := make([]string, 0)
	seen := map[string]struct{}{}
	for _, x := range normalizeNameList(a) {
		if _, ok := set[x]; !ok {
			continue
		}
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}

func riskLevelFor(mode StdioSecurityMode, eff EffectiveSecurity) string {
	switch mode {
	case SecurityModeUnrestricted:
		return "critical"
	case SecurityModeStrict:
		if eff.PolicyOnlyIsolation {
			// 严格档在无内核隔离时仍是策略约束，不能对外宣称低风险。
			return "medium"
		}
		if eff.Network.Mode == NetworkAccessUnrestricted || eff.AllowSelfInstall {
			return "medium"
		}
		return "low"
	default:
		if containsName(eff.CommandAllowlist, "docker") {
			return "high"
		}
		return "medium"
	}
}

func containsName(list []string, name string) bool {
	name = strings.ToLower(name)
	for _, x := range list {
		if x == name {
			return true
		}
	}
	return false
}
