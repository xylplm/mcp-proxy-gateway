package runtime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// ToolStatus 为单个逻辑工具的探测结果。
type ToolStatus struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Warning   string `json:"warning,omitempty"`
}

// Summary 为管理台「运行环境」摘要。
type Summary struct {
	StdioEnabled             bool              `json:"stdioEnabled"`
	CommandAllowlist         []string          `json:"commandAllowlist"`
	StrictCommandAllowlist   []string          `json:"strictCommandAllowlist,omitempty"`
	StrictPackageAllowlist   []string          `json:"strictPackageAllowlist,omitempty"`
	DefaultStdioSecurityMode StdioSecurityMode `json:"defaultStdioSecurityMode"`
	GlobalFileRoots          []string          `json:"globalFileRoots,omitempty"`
	StrictPathOnlyRuntime    bool              `json:"strictPathOnlyRuntime"`
	Tools                    []ToolStatus      `json:"tools"`
	AvailableCount           int               `json:"availableCount"`
	MissingCount             int               `json:"missingCount"`
	DataDir                  string            `json:"dataDir,omitempty"`
	RuntimeDir               string            `json:"runtimeDir,omitempty"`
	PathPrefixes             []string          `json:"pathPrefixes,omitempty"`
	LayoutReady              bool              `json:"layoutReady"`
	ProcessHardening         bool              `json:"processHardening"`
	// ImageFlavor 仅供展示与问题排查；功能门控请用 LocalRuntimeSupported。
	ImageFlavor           string              `json:"imageFlavor"`
	LocalRuntimeSupported bool                `json:"localRuntimeSupported"`
	Sandbox               SandboxCapabilities `json:"sandbox"`
	Deps                  *DepsStatus         `json:"deps,omitempty"`
	RiskNotes             []string            `json:"riskNotes"`
}

// DepsStatus 汇总依赖管理（npm/pip）的进行中状态。
//
// 已装包列表不在此返回（可能较慢，且每次 summary 拉取都会执行 npm/uv）；
// 前端通过 GET /runtime/deps?kind=... 单独按需获取。
type DepsStatus struct {
	DepProgress *DepProgress  `json:"depProgress,omitempty"`
	DepLogs     []DepLogEntry `json:"depLogs,omitempty"`
	DepError    string        `json:"depError,omitempty"`
}

// LookPathFunc 便于单测注入。
type LookPathFunc func(file string) (string, error)

// Doctor 探测宿主工具可用性（仅 LookPath，不执行 --version）。
type Doctor struct {
	lookPath LookPathFunc
	tools    []string
}

// NewDoctor 构造探测器；lookPath 为空时使用 exec.LookPath。
func NewDoctor(lookPath LookPathFunc) *Doctor {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	return &Doctor{
		lookPath: lookPath,
		tools:    DefaultProbeTools(),
	}
}

// Probe 返回 tools 的可用性列表（顺序稳定）。
func (d *Doctor) Probe() []ToolStatus {
	if d == nil {
		return nil
	}
	tools := d.tools
	if len(tools) == 0 {
		tools = DefaultProbeTools()
	}
	out := make([]ToolStatus, 0, len(tools))
	for _, name := range tools {
		path, warning, err := lookPathWithWarning(name, d.lookPath)
		st := ToolStatus{Name: name, Available: err == nil && path != ""}
		if path != "" {
			st.Path = path
		}
		st.Warning = warning
		if warning != "" {
			st.Available = false
		}
		out = append(out, st)
	}
	return out
}

func lookPathWithWarning(name string, lookPath LookPathFunc) (string, string, error) {
	path, err := lookPath(name)
	if err != nil || path == "" {
		return path, "", err
	}
	return path, executablePermissionWarning(path), nil
}

// BuildSummary 组合策略与探测结果。
func BuildSummary(
	policy Policy,
	tools []ToolStatus,
	dataDir, runtimeDir string,
	pathPrefixes []string,
	flavor ImageFlavor,
) Summary {
	policy = NormalizePolicy(policy)
	allowlist := policy.CommandAllowlist
	if allowlist == nil {
		allowlist = DefaultCommandAllowlist()
	}
	available := 0
	for _, t := range tools {
		if t.Available {
			available++
		}
	}
	missing := len(tools) - available
	layoutReady := false
	if runtimeDir != "" {
		if st, err := os.Stat(runtimeDir); err == nil && st.IsDir() {
			if stBin, err := os.Stat(filepath.Join(runtimeDir, RuntimeSubdirBin)); err == nil && stBin.IsDir() {
				layoutReady = true
			}
		}
	}
	strictAllow := policy.StrictCommandAllowlist
	if len(strictAllow) == 0 {
		strictAllow = DefaultStrictCommandAllowlist()
	}
	pkgAllow := policy.StrictPackageAllowlist
	if len(pkgAllow) == 0 {
		pkgAllow = DefaultStrictPackageAllowlist()
	}
	notes := []string{
		"stdio 上游在网关进程旁启动本地子进程，请仅接入可信命令与包来源。",
		"本地运行安全档位（标准 / 严格 / 完全放行）可按上游收敛命令、文件路径与自装包意图；当前为策略约束，不是内核沙箱。",
		"严格模式下 npx/uvx 可执行，但目标包/工具必须落在包白名单内（支持 @scope/*）。",
		"远程 SSE / HTTP / WebSocket / OpenAPI 上游不依赖本页工具探测。",
		"Node / Python / uv 由镜像提供，不在运行期下载；数据卷只保存 npm / pip 共享依赖。",
	}
	if !flavor.LocalRuntimeSupported() {
		notes = append([]string{"当前为精简镜像，不含本地运行时，仅可使用远程与 OpenAPI 上游。"}, notes...)
	} else if !policy.StdioEnabled {
		notes = append([]string{"本地 stdio 上游已禁用，仅可使用远程与 OpenAPI 上游。"}, notes...)
	}
	if missing > 0 && runtimeDir != "" && flavor.LocalRuntimeSupported() {
		binHint := filepath.Join(runtimeDir, RuntimeSubdirBin)
		notes = append(notes,
			"完整镜像已内置 Node / Python / uv；如需覆盖版本，可将可执行文件放入 "+binHint+" 后刷新探测。",
		)
	}
	return Summary{
		StdioEnabled:             policy.StdioEnabled,
		CommandAllowlist:         append([]string{}, allowlist...),
		StrictCommandAllowlist:   append([]string{}, strictAllow...),
		StrictPackageAllowlist:   append([]string{}, pkgAllow...),
		DefaultStdioSecurityMode: policy.DefaultStdioSecurityMode,
		GlobalFileRoots:          append([]string{}, policy.GlobalFileRoots...),
		StrictPathOnlyRuntime:    policy.StrictPathOnlyRuntime,
		Tools:                    tools,
		AvailableCount:           available,
		MissingCount:             missing,
		DataDir:                  dataDir,
		RuntimeDir:               runtimeDir,
		PathPrefixes:             append([]string{}, pathPrefixes...),
		LayoutReady:              layoutReady,
		ProcessHardening:         policy.ProcessHardening,
		ImageFlavor:              string(flavor),
		LocalRuntimeSupported:    flavor.LocalRuntimeSupported(),
		Sandbox:                  DescribeSandbox(),
		RiskNotes:                notes,
	}
}

// Service 聚合策略读取、工具探测与依赖管理，供 HTTP 与 transport 注入。
type Service struct {
	policyFn     func() Policy
	dataDirFn    func() string
	runtimeDirFn func() string
	// flavorFn 可注入以便测试精简镜像下的门控行为。
	flavorFn func() ImageFlavor

	depMu          sync.Mutex
	depMgr         *DependencyManager
	depMgrDir      string
	depOpMu        sync.Mutex // 串行化依赖读写，避免并发 npm/uv 互相覆盖
	layoutMu       sync.Mutex
	layoutDir      string
	preflightMu    sync.Mutex
	preflightCache map[string]preflightCacheEntry
}

// NewService 构造运行时服务。
//
// runtimeDirFn 可空：将由 dataDir + "runtime" 推导。
func NewService(policyFn func() Policy, dataDirFn func() string, runtimeDirFn func() string) *Service {
	if policyFn == nil {
		policyFn = func() Policy { return DefaultPolicy() }
	}
	if dataDirFn == nil {
		dataDirFn = func() string { return os.Getenv("MPG_DATA_DIR") }
	}
	if runtimeDirFn == nil {
		runtimeDirFn = func() string {
			return ResolveRuntimeDir(dataDirFn(), os.Getenv("MPG_RUNTIME_DIR"))
		}
	}
	return &Service{
		policyFn:       policyFn,
		dataDirFn:      dataDirFn,
		runtimeDirFn:   runtimeDirFn,
		preflightCache: make(map[string]preflightCacheEntry),
	}
}

// Flavor 返回当前镜像形态。
func (s *Service) Flavor() ImageFlavor {
	if s != nil && s.flavorFn != nil {
		return s.flavorFn()
	}
	return CurrentImageFlavor()
}

func (s *Service) requireLocalRuntime() error {
	return requireLocalRuntime(s.Flavor())
}

// Policy 返回当前策略快照。
func (s *Service) Policy() Policy {
	if s == nil {
		return DefaultPolicy()
	}
	fn := s.policyFn
	if fn == nil {
		return DefaultPolicy()
	}
	return NormalizePolicy(fn())
}

// RuntimeDir 返回当前卷内运行时根目录。
func (s *Service) RuntimeDir() string {
	if s == nil {
		return ResolveRuntimeDir(os.Getenv("MPG_DATA_DIR"), os.Getenv("MPG_RUNTIME_DIR"))
	}
	if s.runtimeDirFn != nil {
		if dir := strings.TrimSpace(s.runtimeDirFn()); dir != "" {
			return dir
		}
	}
	dataDir := ""
	if s.dataDirFn != nil {
		dataDir = s.dataDirFn()
	}
	return ResolveRuntimeDir(dataDir, os.Getenv("MPG_RUNTIME_DIR"))
}

// depManager 返回绑定当前 runtimeDir 与策略的共享依赖管理器（目录变更时重建）。
func (s *Service) depManager() *DependencyManager {
	if s == nil {
		return NewDependencyManager("", nil)
	}
	dir := s.RuntimeDir()
	s.depMu.Lock()
	defer s.depMu.Unlock()
	if s.depMgr == nil || s.depMgrDir != dir {
		s.depMgr = NewDependencyManager(dir, s.policyFn)
		s.depMgrDir = dir
	}
	return s.depMgr
}

// Summary 返回管理台摘要。
func (s *Service) Summary() Summary {
	if s == nil {
		return BuildSummary(DefaultPolicy(), nil, "", "", nil, CurrentImageFlavor())
	}
	policy := s.Policy()
	dataDir := ""
	if s.dataDirFn != nil {
		dataDir = s.dataDirFn()
	}
	runtimeDir := s.RuntimeDir()
	flavor := s.Flavor()
	s.ensureRuntimeLayout(runtimeDir)
	prefixes := PathPrefixes(runtimeDir)
	doctor := NewDoctor(func(file string) (string, error) {
		return LookPathWithPrefixes(file, prefixes, exec.LookPath)
	})
	summary := BuildSummary(
		policy,
		doctor.Probe(),
		dataDir,
		runtimeDir,
		prefixes,
		flavor,
	)
	// 依赖管理状态：精简镜像无本地运行时，不填充。
	if runtimeDir != "" && flavor.LocalRuntimeSupported() {
		dm := s.depManager()
		summary.Deps = &DepsStatus{
			DepProgress: dm.currentProgress(),
			DepLogs:     dm.Logs(),
			DepError:    dm.lastOpError(),
		}
	}
	return summary
}

func (s *Service) ensureRuntimeLayout(runtimeDir string) {
	runtimeDir = strings.TrimSpace(runtimeDir)
	if runtimeDir == "" {
		return
	}
	s.layoutMu.Lock()
	defer s.layoutMu.Unlock()
	if s.layoutDir == runtimeDir {
		return
	}
	if err := EnsureRuntimeLayout(runtimeDir); err == nil {
		s.layoutDir = runtimeDir
	}
}

// ListDeps 列出某类运行时已安装的第三方包。
func (s *Service) ListDeps(ctx context.Context, kind DepKind) (ListDepsResult, error) {
	if s == nil {
		return ListDepsResult{Kind: kind, Items: []Dependency{}}, errServiceUnavailable
	}
	if err := s.requireLocalRuntime(); err != nil {
		return ListDepsResult{Kind: kind, Items: []Dependency{}}, err
	}
	s.depOpMu.Lock()
	defer s.depOpMu.Unlock()
	return s.depManager().ListDeps(ctx, kind)
}

// InstallDep 安装/升级一个第三方包。
func (s *Service) InstallDep(ctx context.Context, kind DepKind, spec string) (InstallDepResult, error) {
	if s == nil {
		return InstallDepResult{Kind: kind}, errServiceUnavailable
	}
	if err := s.requireLocalRuntime(); err != nil {
		return InstallDepResult{Kind: kind}, err
	}
	s.depOpMu.Lock()
	defer s.depOpMu.Unlock()
	result, err := s.depManager().InstallDep(ctx, kind, spec)
	if err == nil {
		invalidatePathPrefixes(s.RuntimeDir())
		s.InvalidatePreflightCache()
	}
	return result, err
}

// UninstallDep 卸载一个第三方包。
func (s *Service) UninstallDep(ctx context.Context, kind DepKind, name string) (InstallDepResult, error) {
	if s == nil {
		return InstallDepResult{Kind: kind}, errServiceUnavailable
	}
	if err := s.requireLocalRuntime(); err != nil {
		return InstallDepResult{Kind: kind}, err
	}
	s.depOpMu.Lock()
	defer s.depOpMu.Unlock()
	result, err := s.depManager().UninstallDep(ctx, kind, name)
	if err == nil {
		invalidatePathPrefixes(s.RuntimeDir())
		s.InvalidatePreflightCache()
	}
	return result, err
}

// KnownToolCatalog 返回可声明依赖工具字典。
func (s *Service) KnownToolCatalog() []KnownTool {
	return KnownTools()
}

// ValidateStdioCommand 供 transport 校验调用。
func (s *Service) ValidateStdioCommand(command string) error {
	return ValidateCommand(command, s.Policy())
}

// errServiceUnavailable 用于 Service 为 nil 的防御分支（正常接线不会出现）。
var errServiceUnavailable = errors.New("运行环境服务未就绪")
