package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DepKind 标识依赖类型（npm / pip）。
type DepKind string

const (
	DepKindNpm DepKind = "npm"
	DepKindPip DepKind = "pip"
)

// NormalizeDepKind 归一化并校验依赖类型。
func NormalizeDepKind(kind string) (DepKind, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "npm":
		return DepKindNpm, nil
	case "pip":
		return DepKindPip, nil
	default:
		return "", fmt.Errorf("依赖类型仅支持 npm 或 pip")
	}
}

// Dependency 为一个已安装的第三方包。
type Dependency struct {
	Name    string  `json:"name"`
	Version string  `json:"version"`
	Latest  string  `json:"latest,omitempty"`
	Kind    DepKind `json:"kind"`
}

// ListDepsResult 为某类运行时已装第三方包的列表结果。
type ListDepsResult struct {
	Kind       DepKind       `json:"kind"`
	Ready      bool          `json:"ready"`        // 运行时是否可用（npm 已装 / python 解释器存在）
	Items      []Dependency  `json:"items"`        // 已装第三方包（已过滤 npm/pip 内置项）
	Count      int           `json:"count"`        // 第三方包数量
	Warning    string        `json:"warning,omitempty"`
	PythonHint string        `json:"pythonHint,omitempty"` // pip 缺解释器时的引导文案
}

// InstallDepResult 为一次安装/卸载结果。
type InstallDepResult struct {
	Kind    DepKind `json:"kind"`
	Name    string  `json:"name"`
	Version string  `json:"version,omitempty"`
	Message string  `json:"message,omitempty"`
}

// DepProgress 为依赖操作的进行中状态（供前端轮询展示）。
type DepProgress struct {
	Kind      DepKind  `json:"kind"`
	Action    string   `json:"action"` // install | uninstall | list
	Spec      string   `json:"spec,omitempty"`
	StartedAt time.Time `json:"startedAt"`
}

// DepLogLevel 为依赖日志条目级别。
type DepLogLevel string

const (
	DepLogInfo    DepLogLevel = "info"
	DepLogSuccess DepLogLevel = "success"
	DepLogError   DepLogLevel = "error"
)

// DepLogEntry 为一条依赖操作日志（stdout/stderr 逐行 + 阶段标记）。
type DepLogEntry struct {
	Kind    DepKind      `json:"kind"`
	Level   DepLogLevel  `json:"level"`
	Message string       `json:"message"`
	At      time.Time    `json:"at"`
}

const (
	maxDepLogs               = 200
	defaultDepCommandTimeout = 5 * time.Minute
)

// DependencyManager 在 runtimeDir 内对 Node/Python 全局区做包管理。
//
// npm 包通过 npm install --prefix <runtime/node> 装到 Node 自带的全局区，
// stdio node/npx 上游可直接 require；pip 包通过受管 uv 在 runtime/python 建
// 一个共享 venv，python 上游需把该 venv 加入 PATH 或用其解释器。
type DependencyManager struct {
	runtimeDir string
	policyFn   func() Policy

	mu         sync.Mutex // 串行化依赖操作
	progressMu sync.RWMutex
	progress   *DepProgress
	lastError  string
	logMu      sync.RWMutex
	logs       []DepLogEntry
}

// policy 返回当前策略快照（用于构造子进程环境，注入包仓库镜像等）。
func (dm *DependencyManager) policy() Policy {
	if dm == nil {
		return DefaultPolicy()
	}
	if dm.policyFn != nil {
		return NormalizePolicy(dm.policyFn())
	}
	return DefaultPolicy()
}

// NewDependencyManager 构造依赖管理器。
func NewDependencyManager(runtimeDir string, policyFn func() Policy) *DependencyManager {
	return &DependencyManager{
		runtimeDir: strings.TrimSpace(runtimeDir),
		policyFn:   policyFn,
	}
}

// currentProgress 返回进度快照副本。
func (dm *DependencyManager) currentProgress() *DepProgress {
	dm.progressMu.RLock()
	defer dm.progressMu.RUnlock()
	if dm.progress == nil {
		return nil
	}
	cp := *dm.progress
	return &cp
}

// lastOpError 返回最近一次依赖操作失败原因。
func (dm *DependencyManager) lastOpError() string {
	dm.progressMu.RLock()
	defer dm.progressMu.RUnlock()
	return dm.lastError
}

func (dm *DependencyManager) setProgress(p *DepProgress) {
	dm.progressMu.Lock()
	if p == nil {
		dm.progress = nil
	} else {
		cp := *p
		dm.progress = &cp
		dm.lastError = ""
	}
	dm.progressMu.Unlock()
}

func (dm *DependencyManager) setLastError(msg string) {
	dm.progressMu.Lock()
	dm.lastError = msg
	dm.progressMu.Unlock()
}

func (dm *DependencyManager) addLog(entry DepLogEntry) {
	if entry.At.IsZero() {
		entry.At = time.Now().UTC()
	}
	dm.logMu.Lock()
	defer dm.logMu.Unlock()
	dm.logs = append(dm.logs, entry)
	if len(dm.logs) > maxDepLogs {
		dm.logs = append([]DepLogEntry(nil), dm.logs[len(dm.logs)-maxDepLogs:]...)
	}
}

// clearLogs 清空历史依赖日志（新一轮操作开始时调用）。
func (dm *DependencyManager) clearLogs() {
	dm.logMu.Lock()
	defer dm.logMu.Unlock()
	dm.logs = nil
}

// Logs 返回依赖日志副本（最早在前）。
func (dm *DependencyManager) Logs() []DepLogEntry {
	dm.logMu.RLock()
	defer dm.logMu.RUnlock()
	if len(dm.logs) == 0 {
		return nil
	}
	out := make([]DepLogEntry, len(dm.logs))
	copy(out, dm.logs)
	return out
}

// ListDeps 列出某类运行时已安装的第三方包。
func (dm *DependencyManager) ListDeps(ctx context.Context, kind DepKind) (ListDepsResult, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	empty := ListDepsResult{Kind: kind, Items: []Dependency{}}
	if dm.runtimeDir == "" {
		empty.Warning = "运行时目录未配置"
		return empty, nil
	}
	switch kind {
	case DepKindNpm:
		return dm.listNpm(ctx)
	case DepKindPip:
		return dm.listPip(ctx)
	default:
		return empty, fmt.Errorf("不支持的依赖类型 %q", kind)
	}
}

// InstallDep 安装/升级一个包（spec 支持 name@version / name==version / name）。
func (dm *DependencyManager) InstallDep(ctx context.Context, kind DepKind, spec string) (InstallDepResult, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return InstallDepResult{Kind: kind}, fmt.Errorf("包名不能为空")
	}
	if err := validateDepSpec(spec); err != nil {
		return InstallDepResult{Kind: kind}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultDepCommandTimeout)
		defer cancel()
	}
	dm.clearLogs()
	dm.setProgress(&DepProgress{Kind: kind, Action: "install", Spec: spec, StartedAt: time.Now().UTC()})
	defer dm.setProgress(nil)
	dm.addLog(DepLogEntry{Kind: kind, Level: DepLogInfo, Message: "开始安装 " + spec})

	switch kind {
	case DepKindNpm:
		return dm.installNpm(ctx, spec)
	case DepKindPip:
		return dm.installPip(ctx, spec)
	default:
		return InstallDepResult{Kind: kind}, fmt.Errorf("不支持的依赖类型 %q", kind)
	}
}

// UninstallDep 卸载一个包。
func (dm *DependencyManager) UninstallDep(ctx context.Context, kind DepKind, name string) (InstallDepResult, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" {
		return InstallDepResult{Kind: kind}, fmt.Errorf("包名不能为空")
	}
	if err := validateDepSpec(name); err != nil {
		return InstallDepResult{Kind: kind}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultDepCommandTimeout)
		defer cancel()
	}
	dm.clearLogs()
	dm.setProgress(&DepProgress{Kind: kind, Action: "uninstall", Spec: name, StartedAt: time.Now().UTC()})
	defer dm.setProgress(nil)
	dm.addLog(DepLogEntry{Kind: kind, Level: DepLogInfo, Message: "开始卸载 " + name})

	switch kind {
	case DepKindNpm:
		return dm.uninstallNpm(ctx, name)
	case DepKindPip:
		return dm.uninstallPip(ctx, name)
	default:
		return InstallDepResult{Kind: kind}, fmt.Errorf("不支持的依赖类型 %q", kind)
	}
}

// --- npm ---

func (dm *DependencyManager) nodeDir() string {
	return filepath.Join(dm.runtimeDir, RuntimeSubdirNode)
}

func (dm *DependencyManager) resolveNpm() (string, error) {
	prefixes := PathPrefixes(dm.runtimeDir)
	cmd, err := ResolveCommandWithPrefixes("npm", prefixes)
	if err != nil {
		return "", fmt.Errorf("未找到 npm，请先在「运行环境」安装 Node：%w", err)
	}
	return cmd, nil
}

func (dm *DependencyManager) listNpm(ctx context.Context) (ListDepsResult, error) {
	result := ListDepsResult{Kind: DepKindNpm, Items: []Dependency{}}
	npmPath, err := dm.resolveNpm()
	if err != nil {
		result.Warning = err.Error()
		return result, nil
	}
	// npm ls --json --depth=0 列出直接依赖。
	stdout, stderr, runErr := dm.runCommand(ctx, npmPath, []string{"ls", "--json", "--depth=0"}, dm.nodeDir(), DepKindNpm)
	if runErr != nil {
		// npm 在无 package.json 或无依赖时可能返回非零但 stdout 仍含 JSON。
		if stdout == "" {
			result.Warning = fmt.Sprintf("npm ls 失败：%v%s", runErr, depErrTail(stderr))
			return result, nil
		}
	}
	var parsed struct {
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
		Problems []string `json:"problems"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		result.Warning = fmt.Sprintf("解析 npm 输出失败：%v", err)
		return result, nil
	}
	for name, info := range parsed.Dependencies {
		result.Items = append(result.Items, Dependency{Name: name, Version: info.Version, Kind: DepKindNpm})
	}
	result.Ready = true
	result.Count = len(result.Items)
	if len(parsed.Problems) > 0 {
		result.Warning = strings.Join(parsed.Problems, "; ")
	}
	return result, nil
}

func (dm *DependencyManager) installNpm(ctx context.Context, spec string) (InstallDepResult, error) {
	npmPath, err := dm.resolveNpm()
	if err != nil {
		dm.setLastError(err.Error())
		dm.addLog(DepLogEntry{Kind: DepKindNpm, Level: DepLogError, Message: err.Error()})
		return InstallDepResult{Kind: DepKindNpm}, err
	}
	args := []string{"install", "--prefix", dm.nodeDir(), spec}
	_, stderr, runErr := dm.runCommand(ctx, npmPath, args, dm.nodeDir(), DepKindNpm)
	if runErr != nil {
		msg := fmt.Sprintf("npm install 失败：%v%s", runErr, depErrTail(stderr))
		dm.setLastError(msg)
		dm.addLog(DepLogEntry{Kind: DepKindNpm, Level: DepLogError, Message: msg})
		return InstallDepResult{Kind: DepKindNpm}, fmt.Errorf("%s", msg)
	}
	name, ver := parseDepSpecName(spec)
	dm.addLog(DepLogEntry{Kind: DepKindNpm, Level: DepLogSuccess, Message: name + " 安装完成"})
	return InstallDepResult{Kind: DepKindNpm, Name: name, Version: ver}, nil
}

func (dm *DependencyManager) uninstallNpm(ctx context.Context, name string) (InstallDepResult, error) {
	npmPath, err := dm.resolveNpm()
	if err != nil {
		dm.setLastError(err.Error())
		return InstallDepResult{Kind: DepKindNpm}, err
	}
	args := []string{"uninstall", "--prefix", dm.nodeDir(), name}
	_, stderr, runErr := dm.runCommand(ctx, npmPath, args, dm.nodeDir(), DepKindNpm)
	if runErr != nil {
		msg := fmt.Sprintf("npm uninstall 失败：%v%s", runErr, depErrTail(stderr))
		dm.setLastError(msg)
		dm.addLog(DepLogEntry{Kind: DepKindNpm, Level: DepLogError, Message: msg})
		return InstallDepResult{Kind: DepKindNpm}, fmt.Errorf("%s", msg)
	}
	dm.addLog(DepLogEntry{Kind: DepKindNpm, Level: DepLogSuccess, Message: name + " 已卸载"})
	return InstallDepResult{Kind: DepKindNpm, Name: name}, nil
}

// --- pip (via uv) ---

func (dm *DependencyManager) pythonDir() string {
	return filepath.Join(dm.runtimeDir, RuntimeSubdirPython)
}

func (dm *DependencyManager) venvDir() string {
	return filepath.Join(dm.pythonDir(), ".venv")
}

func (dm *DependencyManager) resolveUv() (string, error) {
	prefixes := PathPrefixes(dm.runtimeDir)
	cmd, err := ResolveCommandWithPrefixes("uv", prefixes)
	if err != nil {
		return "", fmt.Errorf("未找到 uv，请先在「运行环境」安装 uv：%w", err)
	}
	return cmd, nil
}

// resolveSystemPython 在系统 PATH 上找一个 python 解释器（uv 受管二进制不含解释器）。
func (dm *DependencyManager) resolveSystemPython() (string, bool) {
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, true
		}
	}
	return "", false
}

// ensureVenv 惰性创建共享 venv；返回 false 表示缺 Python 解释器。
func (dm *DependencyManager) ensureVenv(ctx context.Context) (bool, error) {
	if st, err := os.Stat(filepath.Join(dm.venvDir(), "bin", "python")); err == nil && !st.IsDir() {
		return true, nil
	}
	py, ok := dm.resolveSystemPython()
	if !ok {
		return false, nil
	}
	if err := os.MkdirAll(dm.pythonDir(), 0o755); err != nil {
		return true, err
	}
	uvPath, err := dm.resolveUv()
	if err != nil {
		return true, err
	}
	_, stderr, runErr := dm.runCommand(ctx, uvPath, []string{"venv", "--python", py, dm.venvDir()}, dm.pythonDir(), DepKindPip)
	if runErr != nil {
		return true, fmt.Errorf("创建 venv 失败：%v%s", runErr, depErrTail(stderr))
	}
	return true, nil
}

func (dm *DependencyManager) listPip(ctx context.Context) (ListDepsResult, error) {
	result := ListDepsResult{Kind: DepKindPip, Items: []Dependency{}}
	uvPath, err := dm.resolveUv()
	if err != nil {
		result.Warning = err.Error()
		return result, nil
	}
	venvOK, err := dm.ensureVenv(ctx)
	if err != nil {
		result.Warning = err.Error()
		return result, nil
	}
	if !venvOK {
		result.PythonHint = "未检测到 Python 解释器。uv 不含解释器，请在宿主机安装 Python 3（如 python3）后重试。"
		return result, nil
	}
	stdout, stderr, runErr := dm.runCommand(ctx, uvPath, []string{"pip", "list", "--python", dm.venvDir(), "--format", "json"}, dm.pythonDir(), DepKindPip)
	if runErr != nil {
		result.Warning = fmt.Sprintf("uv pip list 失败：%v%s", runErr, depErrTail(stderr))
		return result, nil
	}
	var parsed []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		result.Warning = fmt.Sprintf("解析 pip 输出失败：%v", err)
		return result, nil
	}
	skip := pipBuiltinNames()
	for _, p := range parsed {
		if _, isBuiltin := skip[strings.ToLower(p.Name)]; isBuiltin {
			continue
		}
		result.Items = append(result.Items, Dependency{Name: p.Name, Version: p.Version, Kind: DepKindPip})
	}
	result.Ready = true
	result.Count = len(result.Items)
	return result, nil
}

func (dm *DependencyManager) installPip(ctx context.Context, spec string) (InstallDepResult, error) {
	uvPath, err := dm.resolveUv()
	if err != nil {
		dm.setLastError(err.Error())
		dm.addLog(DepLogEntry{Kind: DepKindPip, Level: DepLogError, Message: err.Error()})
		return InstallDepResult{Kind: DepKindPip}, err
	}
	venvOK, err := dm.ensureVenv(ctx)
	if err != nil {
		dm.setLastError(err.Error())
		dm.addLog(DepLogEntry{Kind: DepKindPip, Level: DepLogError, Message: err.Error()})
		return InstallDepResult{Kind: DepKindPip}, err
	}
	if !venvOK {
		msg := "未检测到 Python 解释器，无法创建 venv"
		dm.setLastError(msg)
		dm.addLog(DepLogEntry{Kind: DepKindPip, Level: DepLogError, Message: msg})
		return InstallDepResult{Kind: DepKindPip}, fmt.Errorf("%s", msg)
	}
	args := []string{"pip", "install", "--python", dm.venvDir(), specForPip(spec)}
	_, stderr, runErr := dm.runCommand(ctx, uvPath, args, dm.pythonDir(), DepKindPip)
	if runErr != nil {
		msg := fmt.Sprintf("uv pip install 失败：%v%s", runErr, depErrTail(stderr))
		dm.setLastError(msg)
		dm.addLog(DepLogEntry{Kind: DepKindPip, Level: DepLogError, Message: msg})
		return InstallDepResult{Kind: DepKindPip}, fmt.Errorf("%s", msg)
	}
	name, ver := parseDepSpecName(spec)
	dm.addLog(DepLogEntry{Kind: DepKindPip, Level: DepLogSuccess, Message: name + " 安装完成"})
	return InstallDepResult{Kind: DepKindPip, Name: name, Version: ver}, nil
}

func (dm *DependencyManager) uninstallPip(ctx context.Context, name string) (InstallDepResult, error) {
	uvPath, err := dm.resolveUv()
	if err != nil {
		dm.setLastError(err.Error())
		return InstallDepResult{Kind: DepKindPip}, err
	}
	venvOK, err := dm.ensureVenv(ctx)
	if err != nil {
		dm.setLastError(err.Error())
		return InstallDepResult{Kind: DepKindPip}, err
	}
	if !venvOK {
		msg := "未检测到 Python 解释器，无法创建 venv"
		dm.setLastError(msg)
		return InstallDepResult{Kind: DepKindPip}, fmt.Errorf("%s", msg)
	}
	args := []string{"pip", "uninstall", "--python", dm.venvDir(), "-y", name}
	_, stderr, runErr := dm.runCommand(ctx, uvPath, args, dm.pythonDir(), DepKindPip)
	if runErr != nil {
		msg := fmt.Sprintf("uv pip uninstall 失败：%v%s", runErr, depErrTail(stderr))
		dm.setLastError(msg)
		dm.addLog(DepLogEntry{Kind: DepKindPip, Level: DepLogError, Message: msg})
		return InstallDepResult{Kind: DepKindPip}, fmt.Errorf("%s", msg)
	}
	dm.addLog(DepLogEntry{Kind: DepKindPip, Level: DepLogSuccess, Message: name + " 已卸载"})
	return InstallDepResult{Kind: DepKindPip, Name: name}, nil
}

// --- 命令执行 ---

// runCommand 执行一条命令，逐行捕获 stdout/stderr 写入日志缓冲，返回 stdout 全文与 stderr 尾部。
func (dm *DependencyManager) runCommand(ctx context.Context, command string, args []string, cwd string, kind DepKind) (string, string, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	// 复用 env 构造：剥离敏感父进程变量、注入包仓库镜像、前置 runtime PATH。
	cmd.Env = BuildChildEnvWithOptions(os.Environ(), nil, dm.policy(), ChildEnvOptions{
		Mode:       SecurityModeStandard,
		RuntimeDir: dm.runtimeDir,
	}, PathPrefixes(dm.runtimeDir)...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", "", err
	}
	if err := cmd.Start(); err != nil {
		return "", "", err
	}

	var stdoutBuf strings.Builder
	var stderrBuf strings.Builder
	doneOut := make(chan struct{})
	doneErr := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			stdoutBuf.WriteString(line)
			stdoutBuf.WriteByte('\n')
			dm.addLog(DepLogEntry{Kind: kind, Level: DepLogInfo, Message: line})
		}
		close(doneOut)
	}()
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			stderrBuf.WriteString(line)
			stderrBuf.WriteByte('\n')
			// npm/pip 进度信息走 stderr，标注为 info（非错误）。
			dm.addLog(DepLogEntry{Kind: kind, Level: DepLogInfo, Message: line})
		}
		close(doneErr)
	}()

	waitErr := cmd.Wait()
	<-doneOut
	<-doneErr
	return stdoutBuf.String(), stderrBuf.String(), waitErr
}

// --- 辅助 ---

// validateDepSpec 校验包名/spec：拒绝空、路径分隔符、空格、..、控制字符。
// 允许 name、name@version、@scope/name、@scope/name@version、name==version（pip 风格）。
func validateDepSpec(spec string) error {
	if strings.TrimSpace(spec) == "" {
		return fmt.Errorf("包名不能为空")
	}
	if len(spec) > 256 {
		return fmt.Errorf("包名过长（上限 256 字符）")
	}
	// 先取裸包名（去掉 @version / ==version），再校验名字部分。
	name, _ := parseDepSpecName(spec)
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("包名不能为空")
	}
	if len(name) > 214 {
		return fmt.Errorf("包名过长（上限 214 字符）")
	}
	// @scope/name 允许恰好一个斜杠（且在 @scope 之后）；其余含斜杠/反斜杠/空格的拒绝。
	slashCount := strings.Count(name, "/")
	backslashCount := strings.Count(name, "\\")
	if backslashCount > 0 {
		return fmt.Errorf("包名不能包含反斜杠或空格")
	}
	isScoped := strings.HasPrefix(name, "@")
	if isScoped {
		if slashCount != 1 || !strings.HasPrefix(name, "@/") && !strings.Contains(name, "/") {
			// 合法形如 @scope/name：必须 @ 开头 + 恰好一个 /。
		}
		if slashCount != 1 {
			return fmt.Errorf("scoped 包名格式应为 @scope/name")
		}
		parts := strings.SplitN(name, "/", 2)
		scope := parts[0]
		pkg := parts[1]
		if len(scope) < 2 || strings.ContainsAny(pkg, "/") || pkg == "" {
			return fmt.Errorf("scoped 包名格式应为 @scope/name")
		}
	} else if slashCount > 0 || strings.ContainsAny(name, " \\") {
		return fmt.Errorf("包名不能包含路径分隔符或空格")
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("包名非法")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("包名不能包含控制字符")
		}
	}
	return nil
}

// parseDepSpecName 从 spec 中取出裸包名（去掉 @version / ==version）。
func parseDepSpecName(spec string) (name, version string) {
	spec = strings.TrimSpace(spec)
	// pip 风格 name==version
	if idx := strings.Index(spec, "=="); idx > 0 {
		return spec[:idx], spec[idx+2:]
	}
	// npm 风格 name@version（注意 @scope/name 的 @ 在开头）
	at := strings.LastIndex(spec, "@")
	if at > 0 {
		return spec[:at], spec[at+1:]
	}
	return spec, ""
}

// specForPip 把统一 spec（name 或 name@version）转为 pip 语法（name==version）。
func specForPip(spec string) string {
	name, ver := parseDepSpecName(spec)
	if ver == "" {
		return name
	}
	return name + "==" + ver
}

// depErrTail 返回 stderr 的最后 N 行（错误信息聚合用）。
func depErrTail(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	lines := strings.Split(stderr, "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return "\n" + strings.Join(lines, "\n")
}

// pipBuiltinNames 返回需要过滤掉的 pip 自带/标准库包名（小写）。
func pipBuiltinNames() map[string]struct{} {
	list := []string{
		"pip", "setuptools", "wheel", "packaging", "attrs", "certifi",
		"idna", "requests", "urllib3", "charset-normalizer",
	}
	m := make(map[string]struct{}, len(list))
	for _, n := range list {
		m[n] = struct{}{}
	}
	return m
}
