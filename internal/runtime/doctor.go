package runtime

import (
	"os"
	"os/exec"
	"strings"
	"sync"
)

// ToolStatus 为单个逻辑工具的探测结果。
type ToolStatus struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
}

// Summary 为管理台「运行环境」摘要。
type Summary struct {
	StdioEnabled     bool         `json:"stdioEnabled"`
	CommandAllowlist []string     `json:"commandAllowlist"`
	Tools            []ToolStatus `json:"tools"`
	AvailableCount   int          `json:"availableCount"`
	MissingCount     int          `json:"missingCount"`
	DataDir          string       `json:"dataDir,omitempty"`
	RuntimeDir       string       `json:"runtimeDir,omitempty"`
	PathPrefixes     []string     `json:"pathPrefixes,omitempty"`
	LayoutReady      bool         `json:"layoutReady"`
	RiskNotes        []string     `json:"riskNotes"`
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
		path, err := d.lookPath(name)
		st := ToolStatus{Name: name, Available: err == nil && path != ""}
		if st.Available {
			st.Path = path
		}
		out = append(out, st)
	}
	return out
}

// BuildSummary 组合策略与探测结果。
func BuildSummary(policy Policy, tools []ToolStatus, dataDir, runtimeDir string, pathPrefixes []string) Summary {
	policy = NormalizePolicy(policy)
	allowlist := policy.CommandAllowlist
	if allowlist == nil {
		// 展示层：nil 表示沿用产品默认白名单文案。
		allowlist = DefaultCommandAllowlist()
	}
	available := 0
	for _, t := range tools {
		if t.Available {
			available++
		}
	}
	missing := len(tools) - available
	layoutReady := runtimeDir != ""
	if runtimeDir != "" {
		if st, err := os.Stat(runtimeDir); err != nil || !st.IsDir() {
			layoutReady = false
		}
	}
	notes := []string{
		"stdio 上游在网关进程旁启动本地子进程，请仅接入可信命令与包来源。",
		"远程 SSE / HTTP / WebSocket / OpenAPI 上游不依赖本页工具探测。",
	}
	if !policy.StdioEnabled {
		notes = append([]string{"本地 stdio 上游已禁用，仅可使用远程与 OpenAPI 上游。"}, notes...)
	}
	if missing > 0 && runtimeDir != "" {
		notes = append(notes,
			"可将 node、npx、uv 等可执行文件放入 "+runtimeDir+"/bin（或 node/bin 等），重启网关进程后刷新本页。默认镜像不含这些工具，数据卷内安装可在容器更新后保留。",
		)
	}
	if len(pathPrefixes) == 0 && runtimeDir != "" {
		notes = append(notes, "运行时目录已配置，但尚未发现可用的 bin 子目录；请按目录说明放置工具。")
	}
	return Summary{
		StdioEnabled:     policy.StdioEnabled,
		CommandAllowlist: append([]string{}, allowlist...),
		Tools:            tools,
		AvailableCount:   available,
		MissingCount:     missing,
		DataDir:          dataDir,
		RuntimeDir:       runtimeDir,
		PathPrefixes:     append([]string{}, pathPrefixes...),
		LayoutReady:      layoutReady,
		RiskNotes:        notes,
	}
}

// Service 聚合策略读取与探测，供 HTTP 与 transport 注入。
type Service struct {
	mu           sync.RWMutex
	policyFn     func() Policy
	dataDirFn    func() string
	runtimeDirFn func() string
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
		policyFn:     policyFn,
		dataDirFn:    dataDirFn,
		runtimeDirFn: runtimeDirFn,
	}
}

// Policy 返回当前策略快照。
func (s *Service) Policy() Policy {
	if s == nil {
		return DefaultPolicy()
	}
	s.mu.RLock()
	fn := s.policyFn
	s.mu.RUnlock()
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
	s.mu.RLock()
	fn := s.runtimeDirFn
	dataFn := s.dataDirFn
	s.mu.RUnlock()
	if fn != nil {
		if dir := strings.TrimSpace(fn()); dir != "" {
			return dir
		}
	}
	dataDir := ""
	if dataFn != nil {
		dataDir = dataFn()
	}
	return ResolveRuntimeDir(dataDir, os.Getenv("MPG_RUNTIME_DIR"))
}

// Summary 返回管理台摘要。
func (s *Service) Summary() Summary {
	if s == nil {
		return BuildSummary(DefaultPolicy(), nil, "", "", nil)
	}
	policy := s.Policy()
	dataDir := ""
	if s.dataDirFn != nil {
		dataDir = s.dataDirFn()
	}
	runtimeDir := s.RuntimeDir()
	// 本机/容器均幂等确保目录存在，便于用户直接往里放工具。
	_ = EnsureRuntimeLayout(runtimeDir)
	prefixes := PathPrefixes(runtimeDir)
	doctor := NewDoctor(func(file string) (string, error) {
		return LookPathWithPrefixes(file, prefixes, exec.LookPath)
	})
	return BuildSummary(policy, doctor.Probe(), dataDir, runtimeDir, prefixes)
}

// ValidateStdioCommand 供 transport 校验调用。
func (s *Service) ValidateStdioCommand(command string) error {
	return ValidateCommand(command, s.Policy())
}
