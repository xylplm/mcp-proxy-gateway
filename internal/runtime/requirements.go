package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// 连接参数中的依赖声明键（stdio 专用，存于 connParams）。
const ParamRuntimeRequirements = "runtimeRequirements"

// RequirementsMode 为依赖声明模式。
type RequirementsMode string

const (
	// RequirementsAuto 按 command 自动推断（保存的 tools 可作缓存，评估时以推断为准）。
	RequirementsAuto RequirementsMode = "auto"
	// RequirementsManual 以用户勾选 tools 为准。
	RequirementsManual RequirementsMode = "manual"
)

// KnownTool 为可选/可探测的宿主工具元数据。
type KnownTool struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// PackageID 非空表示可通过预置安装补齐。
	PackageID string `json:"packageId,omitempty"`
	// InferFrom 与 TemplateRuntimes 供管理台做输入期间的乐观提示；
	// 实际预检仍以本包的 InferToolsFromCommand 为准。
	InferFrom        []string `json:"inferFrom,omitempty"`
	TemplateRuntimes []string `json:"templateRuntimes,omitempty"`
}

// RuntimeRequirements 为上游声明的运行时依赖。
type RuntimeRequirements struct {
	Mode  RequirementsMode `json:"mode"`
	Tools []string         `json:"tools"`
	Note  string           `json:"note,omitempty"`
}

// PreflightItem 为单个工具的检查结果。
type PreflightItem struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Required  bool   `json:"required"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Fixable   bool   `json:"fixable"`
	PackageID string `json:"packageId,omitempty"`
	Message   string `json:"message,omitempty"`
	Warning   string `json:"warning,omitempty"`
}

// PreflightAction 为可行动补齐建议。
type PreflightAction struct {
	Type      string `json:"type"` // install | open_runtime | open_settings
	PackageID string `json:"packageId,omitempty"`
	Label     string `json:"label"`
}

// PreflightRequest 为依赖预检入参。
type PreflightRequest struct {
	Transport    string               `json:"transport"`
	Command      string               `json:"command"`
	Args         []string             `json:"args,omitempty"`
	Cwd          string               `json:"cwd,omitempty"`
	Requirements *RuntimeRequirements `json:"requirements,omitempty"`
	// SecurityProfile 可选，stdio 本地安全档位与子策略。
	SecurityProfile *SecurityProfile `json:"securityProfile,omitempty"`
	// TemplateRuntimes 可选，来自模板 RuntimeTag（node/docker/uvx/python/local）。
	TemplateRuntimes []string `json:"templateRuntimes,omitempty"`
}

// PreflightResult 为依赖预检结果。
type PreflightResult struct {
	Ready          bool                `json:"ready"`
	Transport      string              `json:"transport"`
	Command        string              `json:"command,omitempty"`
	Requirements   RuntimeRequirements `json:"requirements"`
	SuggestedTools []string            `json:"suggestedTools,omitempty"`
	Items          []PreflightItem     `json:"items"`
	StdioEnabled   bool                `json:"stdioEnabled"`
	CommandAllowed bool                `json:"commandAllowed"`
	CommandError   string              `json:"commandError,omitempty"`
	RuntimeDir     string              `json:"runtimeDir,omitempty"`
	Actions        []PreflightAction   `json:"actions,omitempty"`
	Cached         bool                `json:"cached,omitempty"`
	// 安全档位扩展字段
	SecurityMode  StdioSecurityMode  `json:"securityMode,omitempty"`
	RiskLevel     string             `json:"riskLevel,omitempty"`
	SecurityOK    bool               `json:"securityOk"`
	SecurityError string             `json:"securityError,omitempty"`
	Effective     *EffectiveSecurity `json:"effectiveSecurity,omitempty"`
	FileAccessOK  bool               `json:"fileAccessOk"`
	NetworkPolicy NetworkPolicy      `json:"networkPolicy,omitempty"`
}

// KnownTools 返回可声明/可探测的工具字典（稳定顺序）。
func KnownTools() []KnownTool {
	return []KnownTool{
		{Name: "node", Label: "Node.js", Description: "Node 运行时", PackageID: DefaultNodePackageID, InferFrom: []string{"node", "npx", "npm"}, TemplateRuntimes: []string{"node"}},
		{Name: "npx", Label: "npx", Description: "Node 包执行器", PackageID: DefaultNodePackageID, InferFrom: []string{"npx"}, TemplateRuntimes: []string{"node"}},
		{Name: "npm", Label: "npm", Description: "Node 包管理器", PackageID: DefaultNodePackageID, InferFrom: []string{"npm"}, TemplateRuntimes: []string{"node"}},
		{Name: "python", Label: "Python", Description: "Python 解释器", InferFrom: []string{"python"}, TemplateRuntimes: []string{"python"}},
		{Name: "python3", Label: "Python 3", Description: "Python 3 解释器", InferFrom: []string{"python3"}, TemplateRuntimes: []string{"python"}},
		{Name: "uv", Label: "uv", Description: "Astral uv", PackageID: "uv-0.6.14", InferFrom: []string{"uv", "uvx"}, TemplateRuntimes: []string{"uv", "uvx"}},
		{Name: "uvx", Label: "uvx", Description: "uv 工具运行器", PackageID: "uv-0.6.14", InferFrom: []string{"uvx"}, TemplateRuntimes: []string{"uv", "uvx"}},
		{Name: "docker", Label: "Docker", Description: "容器运行时（需宿主机自行安装）", InferFrom: []string{"docker"}, TemplateRuntimes: []string{"docker"}},
	}
}

func knownToolMap() map[string]KnownTool {
	m := make(map[string]KnownTool, 16)
	for _, t := range KnownTools() {
		m[t.Name] = t
	}
	return m
}

// IsKnownTool 判断是否为合法依赖工具名。
func IsKnownTool(name string) bool {
	_, ok := knownToolMap()[CommandBaseName(name)]
	if ok {
		return true
	}
	// CommandBaseName 对无扩展名逻辑名即小写 trim
	_, ok = knownToolMap()[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// InferToolsFromCommand 根据 command 基名推断建议工具。
func InferToolsFromCommand(command string) []string {
	base := CommandBaseName(command)
	if base == "" {
		return nil
	}
	var out []string
	for _, tool := range KnownTools() {
		for _, source := range tool.InferFrom {
			if source == base {
				out = append(out, tool.Name)
				break
			}
		}
	}
	return out
}

// InferToolsFromTemplateRuntimes 将模板 RuntimeTag 映射为工具名。
func InferToolsFromTemplateRuntimes(tags []string) []string {
	set := map[string]struct{}{}
	for _, raw := range tags {
		runtime := strings.ToLower(strings.TrimSpace(raw))
		if runtime == "" {
			continue
		}
		for _, tool := range KnownTools() {
			for _, source := range tool.TemplateRuntimes {
				if source == runtime {
					set[tool.Name] = struct{}{}
					break
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for _, tool := range KnownTools() {
		if _, ok := set[tool.Name]; ok {
			out = append(out, tool.Name)
		}
	}
	return out
}

// NormalizeRequirements 清洗 mode/tools/note。
func NormalizeRequirements(req RuntimeRequirements) RuntimeRequirements {
	mode := RequirementsMode(strings.ToLower(strings.TrimSpace(string(req.Mode))))
	if mode != RequirementsManual {
		mode = RequirementsAuto
	}
	req.Mode = mode
	req.Note = strings.TrimSpace(req.Note)
	if len(req.Note) > 200 {
		req.Note = req.Note[:200]
	}
	req.Tools = normalizeToolList(req.Tools)
	return req
}

func normalizeToolList(tools []string) []string {
	known := knownToolMap()
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		name := strings.ToLower(strings.TrimSpace(t))
		name = CommandBaseName(name)
		if name == "" {
			continue
		}
		if _, ok := known[name]; !ok {
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

// ValidateRequirements 校验依赖声明结构（不探测宿主）。
// raw 为 connParams 中的值；nil/缺省合法。
func ValidateRequirements(raw any) (RuntimeRequirements, error) {
	if raw == nil {
		return RuntimeRequirements{Mode: RequirementsAuto, Tools: []string{}}, nil
	}
	req, err := ParseRequirements(raw)
	if err != nil {
		return RuntimeRequirements{}, err
	}
	rawMode := strings.ToLower(strings.TrimSpace(string(req.Mode)))
	if rawMode != "" && rawMode != string(RequirementsAuto) && rawMode != string(RequirementsManual) {
		return RuntimeRequirements{}, fmt.Errorf("runtimeRequirements.mode 仅支持 auto 或 manual")
	}
	if len(req.Note) > 200 {
		return RuntimeRequirements{}, fmt.Errorf("依赖备注最多 200 字符")
	}
	if len(req.Tools) > 16 {
		return RuntimeRequirements{}, fmt.Errorf("依赖工具最多 16 项")
	}
	// 再确认无未知项被静默丢弃：若用户传入未知名则报错。
	if err := validateRawToolsField(req.Tools); err != nil {
		return RuntimeRequirements{}, err
	}
	return NormalizeRequirements(req), nil
}

func validateRawToolsField(toolsRaw any) error {
	if toolsRaw == nil {
		return nil
	}
	var original []string
	switch v := toolsRaw.(type) {
	case []string:
		original = v
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return fmt.Errorf("runtimeRequirements.tools 必须为字符串数组")
			}
			original = append(original, s)
		}
	default:
		return fmt.Errorf("runtimeRequirements.tools 必须为字符串数组")
	}
	for _, t := range original {
		name := strings.ToLower(strings.TrimSpace(t))
		if name == "" {
			return fmt.Errorf("依赖工具名不能为空")
		}
		if !IsKnownTool(name) {
			return fmt.Errorf("未知依赖工具 %q（仅支持运行环境探测清单中的工具）", name)
		}
	}
	return nil
}

// ParseRequirements 从 JSON 对象解析依赖声明。
func ParseRequirements(raw any) (RuntimeRequirements, error) {
	switch v := raw.(type) {
	case RuntimeRequirements:
		return v, nil
	case map[string]any:
		req := RuntimeRequirements{Mode: RequirementsAuto}
		if m, ok := v["mode"].(string); ok {
			req.Mode = RequirementsMode(m)
		}
		if n, ok := v["note"].(string); ok {
			req.Note = n
		}
		if toolsRaw, ok := v["tools"]; ok && toolsRaw != nil {
			switch tv := toolsRaw.(type) {
			case []string:
				req.Tools = append([]string{}, tv...)
			case []any:
				for _, item := range tv {
					s, ok := item.(string)
					if !ok {
						return RuntimeRequirements{}, fmt.Errorf("runtimeRequirements.tools 必须为字符串数组")
					}
					req.Tools = append(req.Tools, s)
				}
			default:
				return RuntimeRequirements{}, fmt.Errorf("runtimeRequirements.tools 必须为字符串数组")
			}
		}
		return req, nil
	default:
		return RuntimeRequirements{}, fmt.Errorf("runtimeRequirements 必须为对象")
	}
}

// ResolveEffectiveTools 合并自动推断与用户声明，得到 effective 探测列表。
func ResolveEffectiveTools(command string, req RuntimeRequirements, templateRuntimes []string) (effective, suggested []string) {
	req = NormalizeRequirements(req)
	suggested = mergeTools(InferToolsFromCommand(command), InferToolsFromTemplateRuntimes(templateRuntimes))
	if req.Mode == RequirementsManual {
		if len(req.Tools) == 0 {
			return nil, suggested
		}
		return append([]string{}, req.Tools...), suggested
	}
	// auto：优先推断；若用户曾保存 tools 且推断为空，则回退 tools（绝对路径脚本场景）
	if len(suggested) > 0 {
		return append([]string{}, suggested...), suggested
	}
	return append([]string{}, req.Tools...), suggested
}

func mergeTools(sets ...[]string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, set := range sets {
		for _, t := range normalizeToolList(set) {
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

// EvaluatePreflight 基于策略与 LookPath 评估依赖就绪状态。
func EvaluatePreflight(req PreflightRequest, policy Policy, runtimeDir string, lookPath LookPathFunc) PreflightResult {
	policy = NormalizePolicy(policy)
	transport := strings.TrimSpace(req.Transport)
	command := strings.TrimSpace(req.Command)

	result := PreflightResult{
		Transport:      transport,
		Command:        command,
		StdioEnabled:   policy.StdioEnabled,
		CommandAllowed: true,
		SecurityOK:     true,
		FileAccessOK:   true,
		RuntimeDir:     runtimeDir,
		Requirements:   RuntimeRequirements{Mode: RequirementsAuto, Tools: []string{}},
		Items:          []PreflightItem{},
		Actions:        []PreflightAction{},
	}

	if !strings.EqualFold(transport, "stdio") {
		result.Ready = true
		return result
	}

	userReq := RuntimeRequirements{Mode: RequirementsAuto}
	if req.Requirements != nil {
		userReq = NormalizeRequirements(*req.Requirements)
	}
	result.Requirements = userReq

	var profile SecurityProfile
	if req.SecurityProfile != nil {
		profile = NormalizeSecurityProfile(*req.SecurityProfile)
	}
	eff := ResolveEffectiveSecurity(policy, profile, req.Cwd)
	result.SecurityMode = eff.Mode
	result.RiskLevel = eff.RiskLevel
	result.Effective = &eff
	result.NetworkPolicy = eff.Network

	if err := ValidateCommandForSecurity(command, policy, eff); err != nil {
		result.CommandAllowed = false
		result.CommandError = err.Error()
	}
	if err := ValidateIsolationRequirement(policy, eff); err != nil {
		result.SecurityOK = false
		result.SecurityError = err.Error()
	}

	if err := ValidateEffectiveSecurityWithCommand(eff, req.Cwd, command, req.Args); err != nil {
		result.SecurityOK = false
		result.SecurityError = err.Error()
		// 区分文件类错误便于 UI
		if strings.Contains(err.Error(), "工作目录") || strings.Contains(err.Error(), "文件允许") {
			result.FileAccessOK = false
		}
	}

	// 严格档：依赖声明门禁——manual 且 tools 为空时不 ready（提示用户勾选）
	if eff.Mode == SecurityModeStrict && userReq.Mode == RequirementsManual && len(userReq.Tools) == 0 {
		if result.SecurityOK {
			result.SecurityOK = false
			result.SecurityError = "严格安全模式请声明所需宿主依赖工具"
		}
	}

	effective, suggested := ResolveEffectiveTools(command, userReq, req.TemplateRuntimes)
	result.SuggestedTools = suggested
	result.Requirements.Tools = effective

	if lookPath == nil {
		prefixes := PathPrefixes(runtimeDir)
		if eff.Mode == SecurityModeStrict && (eff.StrictPathOnly || policy.StrictPathOnlyRuntime) {
			// 与真实启动复用相同的严格解析，包含真实路径/符号链接边界校验。
			lookPath = func(file string) (string, error) {
				path, warning, err := ResolveCommandStrictRuntimeStatus(file, prefixes)
				if warning != "" {
					return path, &permissionWarningError{warning: warning}
				}
				return path, err
			}
		} else {
			lookPath = func(file string) (string, error) {
				path, warning, err := LookPathWithPrefixesStatus(file, prefixes, nil)
				if warning != "" {
					return path, &permissionWarningError{warning: warning}
				}
				return path, err
			}
		}
	}

	known := knownToolMap()
	missingFixable := map[string]string{} // packageId -> label
	allAvailable := true
	for _, name := range effective {
		meta := known[name]
		item := PreflightItem{
			Name:      name,
			Label:     meta.Label,
			Required:  true,
			Fixable:   meta.PackageID != "",
			PackageID: meta.PackageID,
		}
		if item.Label == "" {
			item.Label = name
		}
		path, err := lookPath(name)
		if warningErr, ok := err.(*permissionWarningError); ok {
			item.Path = path
			item.Warning = warningErr.warning
			item.Message = warningErr.warning
			allAvailable = false
			result.Items = append(result.Items, item)
			continue
		}
		if err == nil && path != "" {
			item.Available = true
			item.Path = path
		} else {
			allAvailable = false
			item.Available = false
			item.Message = "未在运行时目录或 PATH 中找到"
			if eff.Mode == SecurityModeStrict && (eff.StrictPathOnly || policy.StrictPathOnlyRuntime) {
				item.Message = "未在运行时卷目录中找到（严格模式不使用系统 PATH）"
			}
			if item.Fixable {
				missingFixable[item.PackageID] = meta.Label
			}
		}
		result.Items = append(result.Items, item)
	}

	// 严格档缺依赖视为未就绪；标准/放行同样要求 effective 工具可用（保持原语义）
	result.Ready = result.CommandAllowed && result.StdioEnabled && allAvailable && result.SecurityOK

	if !result.StdioEnabled {
		result.Actions = append(result.Actions, PreflightAction{
			Type:  "open_settings",
			Label: "在系统设置中启用本地 stdio",
		})
	}
	if !result.SecurityOK {
		result.Actions = append(result.Actions, PreflightAction{
			Type:  "open_settings",
			Label: "检查本地运行安全档位与文件/网络策略",
		})
	}
	if len(missingFixable) > 0 {
		pkgIDs := make([]string, 0, len(missingFixable))
		for pkgID := range missingFixable {
			pkgIDs = append(pkgIDs, pkgID)
		}
		sort.Strings(pkgIDs)
		for _, pkgID := range pkgIDs {
			result.Actions = append(result.Actions, PreflightAction{
				Type:      "install",
				PackageID: pkgID,
				Label:     "安装 " + missingFixable[pkgID] + " 运行时",
			})
		}
	}
	if !allAvailable {
		result.Actions = append(result.Actions, PreflightAction{
			Type:  "open_runtime",
			Label: "打开运行环境查看探测结果",
		})
	}
	return result
}

type permissionWarningError struct{ warning string }

func (e *permissionWarningError) Error() string { return e.warning }

// --- Service 级 preflight 缓存（短 TTL，减轻 LookPath 压力）---

type preflightCacheEntry struct {
	at     time.Time
	result PreflightResult
}

const preflightCacheTTL = 15 * time.Second

func preflightCacheKey(req PreflightRequest, runtimeDir string, policy Policy, isolationAvailable bool) string {
	tools := ""
	if req.Requirements != nil {
		tools = strings.Join(req.Requirements.Tools, ",")
		tools = string(req.Requirements.Mode) + "|" + tools
	}
	sec := ""
	if req.SecurityProfile != nil {
		p := NormalizeSecurityProfile(*req.SecurityProfile)
		sec = strings.Join([]string{
			string(p.Mode),
			string(p.FileAccess.Mode),
			strings.Join(p.FileAccess.Paths, ","),
			string(p.Network.Mode),
			strings.Join(p.Network.Hosts, ","),
			string(p.DependencyPolicy),
			strings.Join(p.PackageAllowlist, ","),
			fmt.Sprintf("%v", p.AllowSelfInstall),
		}, "|")
	}
	raw := strings.Join([]string{
		req.Transport,
		req.Command,
		strings.Join(req.Args, " "),
		req.Cwd,
		tools,
		sec,
		strings.Join(req.TemplateRuntimes, ","),
		runtimeDir,
		fmt.Sprintf("%v", isolationAvailable),
		runtimeDirFingerprint(runtimeDir),
		fmt.Sprintf("%v", policy.StdioEnabled),
		string(policy.DefaultStdioSecurityMode),
		strings.Join(policy.CommandAllowlist, ","),
		strings.Join(policy.StrictCommandAllowlist, ","),
		strings.Join(policy.StrictPackageAllowlist, ","),
		strings.Join(policy.GlobalFileRoots, ","),
		fmt.Sprintf("%v", policy.StrictPathOnlyRuntime),
		fmt.Sprintf("%v", policy.StrictAllowPolicyOnly),
	}, "#")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// runtimeDirFingerprint 以 PATH 前缀目录的修改时间作为轻量目录状态指纹。
// 目录新增/删除运行时工具时通常会更新目录 mtime，避免手动放置工具后继续命中旧缓存。
func runtimeDirFingerprint(runtimeDir string) string {
	var b strings.Builder
	for _, dir := range PathPrefixes(runtimeDir) {
		if st, err := os.Stat(dir); err == nil {
			fmt.Fprintf(&b, "%s:%d;", dir, st.ModTime().UnixNano())
		}
	}
	return b.String()
}

// Preflight 执行依赖预检（可缓存）。
func (s *Service) Preflight(req PreflightRequest) PreflightResult {
	if s == nil {
		return EvaluatePreflight(req, DefaultPolicy(), "", nil)
	}
	policy := s.Policy()
	runtimeDir := s.RuntimeDir()
	key := preflightCacheKey(req, runtimeDir, policy, IsolationAvailable())
	s.preflightMu.Lock()
	if s.preflightCache == nil {
		s.preflightCache = make(map[string]preflightCacheEntry)
	}
	if ent, ok := s.preflightCache[key]; ok && time.Since(ent.at) < preflightCacheTTL {
		res := ent.result
		res.Cached = true
		s.preflightMu.Unlock()
		return res
	}
	s.preflightMu.Unlock()

	// EvaluatePreflight 根据生效安全档位选择与真实启动一致的解析器。
	res := EvaluatePreflight(req, policy, runtimeDir, nil)

	s.preflightMu.Lock()
	// 简单防膨胀
	if len(s.preflightCache) > 256 {
		s.preflightCache = map[string]preflightCacheEntry{}
	}
	s.preflightCache[key] = preflightCacheEntry{at: time.Now(), result: res}
	s.preflightMu.Unlock()
	return res
}

// InvalidatePreflightCache 清除当前运行时服务的预检缓存。
func (s *Service) InvalidatePreflightCache() {
	if s == nil {
		return
	}
	s.preflightMu.Lock()
	s.preflightCache = make(map[string]preflightCacheEntry)
	s.preflightMu.Unlock()
}
