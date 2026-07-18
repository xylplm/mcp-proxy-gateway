package runtime

import (
	"os"
	"os/exec"
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
func BuildSummary(policy Policy, tools []ToolStatus, dataDir string) Summary {
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
	notes := []string{
		"stdio 上游在网关进程旁启动本地子进程，请仅接入可信命令与包来源。",
		"远程 SSE / HTTP / WebSocket / OpenAPI 上游不依赖本页工具探测。",
	}
	if !policy.StdioEnabled {
		notes = append([]string{"本地 stdio 上游已禁用，仅可使用远程与 OpenAPI 上游。"}, notes...)
	}
	return Summary{
		StdioEnabled:     policy.StdioEnabled,
		CommandAllowlist: append([]string{}, allowlist...),
		Tools:            tools,
		AvailableCount:   available,
		MissingCount:     len(tools) - available,
		DataDir:          dataDir,
		RiskNotes:        notes,
	}
}

// Service 聚合策略读取与探测，供 HTTP 与 transport 注入。
type Service struct {
	mu        sync.RWMutex
	policyFn  func() Policy
	dataDirFn func() string
	doctor    *Doctor
}

// NewService 构造运行时服务。
func NewService(policyFn func() Policy, dataDirFn func() string) *Service {
	if policyFn == nil {
		policyFn = func() Policy { return DefaultPolicy() }
	}
	if dataDirFn == nil {
		dataDirFn = func() string { return os.Getenv("MPG_DATA_DIR") }
	}
	return &Service{
		policyFn:  policyFn,
		dataDirFn: dataDirFn,
		doctor:    NewDoctor(nil),
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

// Summary 返回管理台摘要。
func (s *Service) Summary() Summary {
	if s == nil {
		return BuildSummary(DefaultPolicy(), nil, "")
	}
	policy := s.Policy()
	dataDir := ""
	if s.dataDirFn != nil {
		dataDir = s.dataDirFn()
	}
	var tools []ToolStatus
	if s.doctor != nil {
		tools = s.doctor.Probe()
	}
	return BuildSummary(policy, tools, dataDir)
}

// ValidateStdioCommand 供 transport 校验调用。
func (s *Service) ValidateStdioCommand(command string) error {
	return ValidateCommand(command, s.Policy())
}
