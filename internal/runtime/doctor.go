package runtime

import (
	"context"
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
	StdioEnabled             bool                `json:"stdioEnabled"`
	CommandAllowlist         []string            `json:"commandAllowlist"`
	StrictCommandAllowlist   []string            `json:"strictCommandAllowlist,omitempty"`
	StrictPackageAllowlist   []string            `json:"strictPackageAllowlist,omitempty"`
	DefaultStdioSecurityMode StdioSecurityMode   `json:"defaultStdioSecurityMode"`
	GlobalFileRoots          []string            `json:"globalFileRoots,omitempty"`
	StrictPathOnlyRuntime    bool                `json:"strictPathOnlyRuntime"`
	Tools                    []ToolStatus        `json:"tools"`
	AvailableCount           int                 `json:"availableCount"`
	MissingCount             int                 `json:"missingCount"`
	DataDir                  string              `json:"dataDir,omitempty"`
	RuntimeDir               string              `json:"runtimeDir,omitempty"`
	PathPrefixes             []string            `json:"pathPrefixes,omitempty"`
	LayoutReady              bool                `json:"layoutReady"`
	ProcessHardening         bool                `json:"processHardening"`
	Sandbox                  SandboxCapabilities `json:"sandbox"`
	Catalog                  []CatalogPackage    `json:"catalog,omitempty"`
	InstalledPackages        []InstallRecord     `json:"installedPackages,omitempty"`
	InstallProgress          *InstallProgress    `json:"installProgress,omitempty"`
	RiskNotes                []string            `json:"riskNotes"`
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
	catalog []CatalogPackage,
	installed []InstallRecord,
	installProgress *InstallProgress,
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
		"预置安装仅允许内置目录中的 Node / uv 固定版本，禁止任意 URL 或 npm 包名。",
	}
	if !policy.StdioEnabled {
		notes = append([]string{"本地 stdio 上游已禁用，仅可使用远程与 OpenAPI 上游。"}, notes...)
	}
	if missing > 0 && runtimeDir != "" {
		binHint := filepath.Join(runtimeDir, RuntimeSubdirBin)
		notes = append(notes,
			"可将工具放入 "+binHint+"，或使用本页「预置安装」拉取官方 Node / uv；完成后刷新探测即可。",
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
		Sandbox:                  DescribeSandbox(),
		Catalog:                  catalog,
		InstalledPackages:        installed,
		InstallProgress:          installProgress,
		RiskNotes:                notes,
	}
}

// Service 聚合策略读取、探测与受控安装，供 HTTP 与 transport 注入。
//
// 持有单一 Installer 实例，保证安装/卸载串行化，避免并发写卷。
type Service struct {
	policyFn     func() Policy
	dataDirFn    func() string
	runtimeDirFn func() string

	instMu         sync.Mutex
	installer_     *Installer
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

// installer 返回绑定当前 runtimeDir 的共享 Installer（目录变更时重建）。
func (s *Service) installer() *Installer {
	if s == nil {
		return NewInstaller("", nil)
	}
	dir := s.RuntimeDir()
	s.instMu.Lock()
	defer s.instMu.Unlock()
	if s.installer_ == nil || s.installer_.runtimeDir != dir {
		s.installer_ = NewInstaller(dir, nil)
	}
	return s.installer_
}

// Summary 返回管理台摘要。
func (s *Service) Summary() Summary {
	if s == nil {
		return BuildSummary(DefaultPolicy(), nil, "", "", nil, nil, nil, nil)
	}
	policy := s.Policy()
	dataDir := ""
	if s.dataDirFn != nil {
		dataDir = s.dataDirFn()
	}
	runtimeDir := s.RuntimeDir()
	s.ensureRuntimeLayout(runtimeDir)
	prefixes := PathPrefixes(runtimeDir)
	doctor := NewDoctor(func(file string) (string, error) {
		return LookPathWithPrefixes(file, prefixes, exec.LookPath)
	})
	inst := s.installer()
	state := inst.loadState()
	progress := inst.currentProgress()
	return BuildSummary(
		policy,
		doctor.Probe(),
		dataDir,
		runtimeDir,
		prefixes,
		inst.catalogWithState(state),
		state.Packages,
		progress,
	)
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

// Catalog 返回预置包目录与安装状态。
func (s *Service) Catalog() []CatalogPackage {
	if s == nil {
		return nil
	}
	return s.installer().CatalogWithStatus()
}

// PreviewInstall 预览安装。
func (s *Service) PreviewInstall(packageID string) (CatalogPackage, error) {
	if s == nil {
		return CatalogPackage{}, fmtUnavailable("运行环境服务未就绪")
	}
	return s.installer().PreviewInstall(packageID)
}

// InstallPackage 执行受控安装（进程内串行）。
func (s *Service) InstallPackage(ctx context.Context, packageID string) (InstallResult, error) {
	if s == nil {
		return InstallResult{}, fmtUnavailable("运行环境服务未就绪")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	res, err := s.installer().Install(ctx, packageID)
	if err == nil {
		s.InvalidatePreflightCache()
	}
	return res, err
}

// UninstallPackage 卸载预置包（进程内串行）。
func (s *Service) UninstallPackage(packageID string) error {
	if s == nil {
		return fmtUnavailable("运行环境服务未就绪")
	}
	err := s.installer().Uninstall(packageID)
	if err == nil {
		s.InvalidatePreflightCache()
	}
	return err
}

// KnownToolCatalog 返回可声明依赖工具字典。
func (s *Service) KnownToolCatalog() []KnownTool {
	return KnownTools()
}

// ValidateStdioCommand 供 transport 校验调用。
func (s *Service) ValidateStdioCommand(command string) error {
	return ValidateCommand(command, s.Policy())
}

func fmtUnavailable(msg string) error {
	return &simpleError{s: msg}
}

type simpleError struct{ s string }

func (e *simpleError) Error() string { return e.s }
