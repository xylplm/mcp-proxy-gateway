package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// LookPathWithPrefixes 优先在 prefixes 目录中查找可执行文件，再回退 lookPath（默认 exec.LookPath）。
//
// 若 file 含路径分隔符或为绝对路径，则不扫描 prefixes，直接走 lookPath。
func LookPathWithPrefixes(file string, prefixes []string, lookPath LookPathFunc) (string, error) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	raw := strings.TrimSpace(file)
	if raw == "" {
		return "", fmt.Errorf("连接参数 \"command\" 不能为空")
	}
	if hasPathSep(raw) || filepath.IsAbs(raw) {
		return lookPath(raw)
	}
	for _, dir := range prefixes {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if p, ok := findExecutableInDir(dir, raw); ok {
			// 再经 lookPath 规范化（处理 Windows 可执行判断等）；失败则仍返回候选路径。
			if resolved, err := lookPath(p); err == nil && resolved != "" {
				return resolved, nil
			}
			return p, nil
		}
	}
	return lookPath(raw)
}

// LookPathWithPrefixesStatus 返回解析路径及可执行权限提示。
// Windows 没有统一的 Unix 执行位，因此权限提示仅在其他平台产生。
func LookPathWithPrefixesStatus(file string, prefixes []string, lookPath LookPathFunc) (string, string, error) {
	path, err := LookPathWithPrefixes(file, prefixes, lookPath)
	if err != nil {
		return "", "", err
	}
	return path, executablePermissionWarning(path), nil
}

// ErrNotInRuntimePath 表示严格模式下命令不在受信运行时目录内。
var ErrNotInRuntimePath = fmt.Errorf("严格安全模式仅允许运行时卷或镜像内置的可执行文件")

// imageRuntimeRoots 返回镜像内置解释器所在目录（去重，已解析符号链接）。
//
// 运行时改由镜像提供后，严格档的受信范围不能只剩运行时卷，否则内置的
// node / python / uv 一个都解析不到，严格档等于不可用。这里按 DefaultProbeTools()
// 在系统 PATH 上反查，镜像调整布局（例如 Node 从 /opt/node 挪走）无需改代码；
// 精简镜像查不到任何工具，集合为空，严格档自然退回「仅卷内」。
//
// 放宽是惰性的：严格档仍要求命令基名落在 strict_command_allowlist 内，
// 因此同目录下的其他可执行文件不会因此变得可启动。
//
// 不做缓存：一次调用最多几十个 stat，相比 stdio 会话建立（fork + MCP initialize）
// 可忽略，省掉一份缓存与失效逻辑。
func imageRuntimeRoots() []string {
	tools := DefaultProbeTools()
	roots := make([]string, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, name := range tools {
		path, err := exec.LookPath(name)
		if err != nil || path == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue
		}
		dir := filepath.Dir(resolved)
		key := rootKey(dir)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		roots = append(roots, dir)
	}
	return roots
}

// strictAllowedRoots 返回严格档允许的可执行文件根：运行时卷 ∪ 镜像解释器目录。
func strictAllowedRoots(prefixes []string) []string {
	roots := strictRuntimeRoots(prefixes)
	seen := make(map[string]struct{}, len(roots))
	for _, r := range roots {
		seen[rootKey(r)] = struct{}{}
	}
	for _, r := range imageRuntimeRoots() {
		key := rootKey(r)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		roots = append(roots, r)
	}
	return roots
}

// rootKey 归一化根目录比较键（Windows 路径大小写不敏感）。
func rootKey(p string) string {
	clean := filepath.Clean(p)
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}

// ResolveCommandWithPrefixes 在 prefixes + PATH 上解析 command，错误文案与 ResolveCommand 一致。
func ResolveCommandWithPrefixes(command string, prefixes []string) (string, error) {
	raw := strings.TrimSpace(command)
	if raw == "" {
		return "", fmt.Errorf("连接参数 \"command\" 不能为空")
	}
	resolved, err := LookPathWithPrefixes(raw, prefixes, exec.LookPath)
	if err != nil {
		base := CommandBaseName(raw)
		if base == "" {
			base = raw
		}
		return "", fmt.Errorf(
			"未找到可执行文件 %q。当前环境缺少该工具，请安装对应运行时或改用远程 MCP。可将工具放入运行时目录 bin 后重启，或在「运行环境」查看探测结果",
			base,
		)
	}
	return resolved, nil
}

// ResolveCommandStrictRuntime 在受信运行时目录内解析 command（严格档）。
//
// 受信目录 = 运行时卷前缀 ∪ 镜像内置解释器目录（见 strictAllowedRoots）。
// 解析顺序：卷内前缀优先（用户放置可覆盖镜像版本），再回落系统 PATH；
// 两条路径的最终真实路径都必须落在受信目录内，符号链接越界一律拒绝。
func ResolveCommandStrictRuntime(command string, prefixes []string) (string, error) {
	raw := strings.TrimSpace(command)
	if raw == "" {
		return "", fmt.Errorf("连接参数 \"command\" 不能为空")
	}
	allowedRoots := strictAllowedRoots(prefixes)
	if len(allowedRoots) == 0 {
		return "", fmt.Errorf("%w：未配置可用的运行时目录", ErrNotInRuntimePath)
	}
	if hasPathSep(raw) || filepath.IsAbs(raw) {
		if resolved, ok := resolveExecutableWithinRoots(raw, allowedRoots); ok {
			return resolved, nil
		}
		return "", fmt.Errorf("%w：%s", ErrNotInRuntimePath, raw)
	}
	for _, dir := range prefixes {
		if p, ok := findExecutableInDir(dir, raw); ok {
			if resolved, allowed := resolveExecutableWithinRoots(p, allowedRoots); allowed {
				return resolved, nil
			}
		}
	}
	// 镜像内置解释器不在卷内，必须允许按名回落系统 PATH；受信根校验仍然生效。
	if p, err := exec.LookPath(raw); err == nil && p != "" {
		if resolved, allowed := resolveExecutableWithinRoots(p, allowedRoots); allowed {
			return resolved, nil
		}
	}
	base := CommandBaseName(raw)
	if base == "" {
		base = raw
	}
	return "", fmt.Errorf(
		"严格模式下未在受信运行时目录找到 %q。完整镜像已内置 node/python/uv；"+
			"精简镜像请将该工具放入 runtime/bin，或改用标准安全档位",
		base,
	)
}

// ResolveCommandStrictRuntimeStatus 返回严格解析路径及可执行权限提示。
func ResolveCommandStrictRuntimeStatus(command string, prefixes []string) (string, string, error) {
	path, err := ResolveCommandStrictRuntime(command, prefixes)
	if err != nil {
		return "", "", err
	}
	return path, executablePermissionWarning(path), nil
}

// ResolveCommand 将 command 解析为可执行路径（仅系统 PATH，兼容旧调用）。
func ResolveCommand(command string) (string, error) {
	return ResolveCommandWithPrefixes(command, nil)
}

// runtimeRootOfPrefix 由一个 PATH 前缀目录反推严格档允许的运行时根。
//
// 做法是剥掉与 runtimePathCandidates 同源的相对后缀（`bin`、
// `npm/node_modules/.bin`、`pip/bin`），而不是按目录名逐级上跳 —— 后者在运行时目录
// 本身叫 pip/npm 时会把父目录也放进允许范围。
//
// 根必须比前缀宽一层：npm 的 `.bin` 条目是指向 `../<pkg>/bin/*.js` 的符号链接，
// 解析后落在 node_modules 内而不在 `.bin` 内，根取到卷根才不会被误判越界。
// 多个后缀同时匹配时取最长的根，即最窄的允许范围。
// 没有任何后缀匹配（调用方传入了非本包生成的前缀）时退回前缀自身，保持最保守。
func runtimeRootOfPrefix(prefix string) string {
	best := ""
	for _, suffix := range runtimePathSuffixes() {
		root, ok := trimPathSuffix(prefix, suffix)
		if !ok {
			continue
		}
		if len(root) > len(best) {
			best = root
		}
	}
	if best == "" {
		return prefix
	}
	return best
}

// trimPathSuffix 在路径分隔符边界上剥掉 suffix，返回剩余前缀。
// 带上分隔符比较，避免 ".../mybin" 被 "bin" 误匹配。
func trimPathSuffix(path, suffix string) (string, bool) {
	clean := filepath.Clean(path)
	tail := string(filepath.Separator) + filepath.Clean(suffix)
	if len(clean) <= len(tail) {
		return "", false
	}
	got := clean[len(clean)-len(tail):]
	// Windows 路径大小写不敏感；后缀均为 ASCII，按字节切片安全。
	if runtime.GOOS == "windows" {
		if !strings.EqualFold(got, tail) {
			return "", false
		}
	} else if got != tail {
		return "", false
	}
	return clean[:len(clean)-len(tail)], true
}

func strictRuntimeRoots(prefixes []string) []string {
	roots := make([]string, 0, len(prefixes))
	seen := map[string]struct{}{}
	for _, dir := range prefixes {
		clean := cleanPathDecl(dir)
		if clean == "" {
			continue
		}
		rootResolved, err := filepath.EvalSymlinks(runtimeRootOfPrefix(clean))
		if err != nil {
			continue
		}
		prefixResolved, err := filepath.EvalSymlinks(clean)
		if err != nil || !pathInRoot(prefixResolved, rootResolved) {
			continue
		}
		rootResolved = filepath.Clean(rootResolved)
		key := rootKey(rootResolved)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		roots = append(roots, rootResolved)
	}
	return roots
}

func resolveExecutableWithinRoots(path string, roots []string) (string, bool) {
	clean := cleanPathDecl(path)
	if clean == "" {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", false
	}
	st, err := os.Stat(resolved)
	if err != nil || st.IsDir() {
		return "", false
	}
	for _, root := range roots {
		if pathInRoot(resolved, root) {
			return resolved, true
		}
	}
	return "", false
}

func hasPathSep(s string) bool {
	return strings.ContainsRune(s, '/') || strings.ContainsRune(s, filepath.Separator) ||
		(filepath.Separator != '/' && strings.ContainsRune(s, '\\'))
}

func findExecutableInDir(dir, name string) (string, bool) {
	base := filepath.Base(name)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", false
	}
	candidates := []string{filepath.Join(dir, base)}
	if runtime.GOOS == "windows" {
		lower := strings.ToLower(base)
		hasExt := false
		for _, ext := range []string{".exe", ".cmd", ".bat", ".com"} {
			if strings.HasSuffix(lower, ext) {
				hasExt = true
				break
			}
		}
		if !hasExt {
			for _, ext := range []string{".exe", ".cmd", ".bat", ".com"} {
				candidates = append(candidates, filepath.Join(dir, base+ext))
			}
		}
	}
	for _, c := range candidates {
		st, err := os.Stat(c)
		if err != nil || st.IsDir() {
			continue
		}
		return c, true
	}
	return "", false
}

func executablePermissionWarning(path string) string {
	if runtime.GOOS == "windows" || strings.TrimSpace(path) == "" {
		return ""
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Mode()&0o111 != 0 {
		return ""
	}
	return "已找到文件但缺少可执行权限，请执行 chmod +x " + path
}
