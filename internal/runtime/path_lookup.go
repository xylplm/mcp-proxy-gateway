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

// ErrNotInRuntimePath 表示严格模式下命令不在 runtime 卷路径内。
var ErrNotInRuntimePath = fmt.Errorf("严格安全模式仅允许运行时卷内的可执行文件")

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

// ResolveCommandStrictRuntime 仅在 runtime 前缀目录中解析 command（严格档）。
// 绝对路径须落在某一 prefix 根下；逻辑名只在 prefix 内查找，不回落系统 PATH。
func ResolveCommandStrictRuntime(command string, prefixes []string) (string, error) {
	raw := strings.TrimSpace(command)
	if raw == "" {
		return "", fmt.Errorf("连接参数 \"command\" 不能为空")
	}
	allowedRoots := strictRuntimeRoots(prefixes)
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
	base := CommandBaseName(raw)
	if base == "" {
		base = raw
	}
	return "", fmt.Errorf(
		"严格模式下未在运行时目录找到 %q，请使用「运行环境」预置安装或将工具放入 runtime/bin",
		base,
	)
}

// ResolveCommand 将 command 解析为可执行路径（仅系统 PATH，兼容旧调用）。
func ResolveCommand(command string) (string, error) {
	return ResolveCommandWithPrefixes(command, nil)
}

func strictRuntimeRoots(prefixes []string) []string {
	roots := make([]string, 0, len(prefixes))
	seen := map[string]struct{}{}
	for _, dir := range prefixes {
		clean := cleanPathDecl(dir)
		if clean == "" {
			continue
		}
		parent := filepath.Dir(clean)
		runtimeRoot := parent
		switch strings.ToLower(filepath.Base(parent)) {
		case RuntimeSubdirNode, RuntimeSubdirPython, RuntimeSubdirUV:
			runtimeRoot = filepath.Dir(parent)
		}
		rootResolved, err := filepath.EvalSymlinks(runtimeRoot)
		if err != nil {
			continue
		}
		prefixResolved, err := filepath.EvalSymlinks(clean)
		if err != nil || !pathInRoot(prefixResolved, rootResolved) {
			continue
		}
		rootResolved = filepath.Clean(rootResolved)
		key := rootResolved
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
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
		// Unix：尽量尊重可执行位；Windows 无统一 exec bit，存在即可。
		if runtime.GOOS != "windows" {
			if st.Mode()&0o111 == 0 {
				// 卷内用户拷贝的二进制偶发无 +x，仍允许作为候选（由 OS 最终判定）。
				// 保持发现能力，避免「文件在却探测不到」。
			}
		}
		return c, true
	}
	return "", false
}
