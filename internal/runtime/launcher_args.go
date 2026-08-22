package runtime

import (
	"fmt"
	"strings"
)

// DefaultStrictPackageAllowlist 严格档默认允许的 npx/uvx 包或工具名。
// 与内置模板常用包对齐；支持 @scope/* 前缀匹配。
func DefaultStrictPackageAllowlist() []string {
	return []string{
		// npm 生态（npx）
		"@modelcontextprotocol/*",
		"@playwright/mcp",
		"@notionhq/notion-mcp-server",
		"firecrawl-mcp",
		"exa-mcp-server",
		// Python 生态（uvx）：官方 MCP 服务在 PyPI 上以 mcp-server-<name> 发布。
		// 只列纯 Python、无外部二进制依赖的，确保完整镜像里开箱可跑。
		"mcp-server-fetch",
		"mcp-server-sqlite",
		"mcp-server-time",
	}
}

// ExtractLauncherTarget 从 npx/uvx 参数中提取拟执行的包/工具标识（不含版本）。
// 无法识别时返回空字符串与 false（调用方按策略决定是否拒绝）。
func ExtractLauncherTarget(command string, args []string) (target string, ok bool, err error) {
	base := CommandBaseName(command)
	switch base {
	case "npx":
		return extractNpxTarget(args)
	case "uvx":
		return extractUvxTarget(args)
	default:
		return "", false, nil
	}
}

// ValidateStrictLauncherTarget 在严格档校验 npx/uvx 目标是否落在包白名单内。
// 非 npx/uvx 命令直接通过。
func ValidateStrictLauncherTarget(command string, args []string, allowlist []string) error {
	base := CommandBaseName(command)
	if base != "npx" && base != "uvx" {
		return nil
	}
	target, found, err := ExtractLauncherTarget(command, args)
	if err != nil {
		return err
	}
	if !found || target == "" {
		return fmt.Errorf("严格安全模式下 %s 必须指定明确的包/工具名，且不得使用本地路径或 URL", base)
	}
	if err := rejectDangerousPackageSpec(target); err != nil {
		return err
	}
	allow := normalizePackageAllowlist(allowlist)
	if len(allow) == 0 {
		allow = normalizePackageAllowlist(DefaultStrictPackageAllowlist())
	}
	if !packageAllowed(target, allow) {
		return fmt.Errorf(
			"严格安全模式禁止 %s 执行未在白名单中的包/工具 %q；请在系统设置或本上游 packageAllowlist 中追加",
			base,
			target,
		)
	}
	return nil
}

func extractNpxTarget(args []string) (string, bool, error) {
	// 危险/禁止在严格档作为启动方式的 flag
	for i := range args {
		a := strings.TrimSpace(args[i])
		al := strings.ToLower(a)
		switch {
		case al == "-p" || al == "--package":
			if i+1 >= len(args) {
				return "", false, fmt.Errorf("npx %s 缺少包名参数", a)
			}
			spec := strings.TrimSpace(args[i+1])
			if err := rejectDangerousPackageSpec(spec); err != nil {
				return "", false, err
			}
			return stripPackageVersion(spec), true, nil
		case strings.HasPrefix(al, "--package="):
			spec := strings.TrimSpace(a[len("--package="):])
			if err := rejectDangerousPackageSpec(spec); err != nil {
				return "", false, err
			}
			return stripPackageVersion(spec), true, nil
		case al == "-c" || al == "--call" || al == "--node-options" || strings.HasPrefix(al, "--node-options="):
			return "", false, fmt.Errorf("严格安全模式禁止 npx 使用 %s", a)
		case al == "--shell" || al == "--shell-auto-fallback":
			return "", false, fmt.Errorf("严格安全模式禁止 npx 使用 shell 相关参数")
		}
	}

	skipNext := false
	for i := range args {
		if skipNext {
			skipNext = false
			continue
		}
		a := strings.TrimSpace(args[i])
		if a == "" {
			continue
		}
		al := strings.ToLower(a)
		// 布尔/无需值的常见 flag
		switch al {
		case "-y", "--yes", "-q", "--quiet", "--prefer-offline", "--prefer-online",
			"--no-install", "--ignore-existing", "--no", "--offline", "-g", "--global":
			continue
		}
		// 带值 flag：跳过下一参数
		if al == "--cache" || al == "--userconfig" || al == "--prefix" || al == "--registry" ||
			al == "--script-shell" || al == "-w" || al == "--workspace" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(a, "-") {
			// 未知 flag：保守拒绝，避免 -e 类绕过
			if strings.Contains(al, "package") {
				continue
			}
			return "", false, fmt.Errorf("严格安全模式不支持的 npx 参数 %q", a)
		}
		if err := rejectDangerousPackageSpec(a); err != nil {
			return "", false, err
		}
		return stripPackageVersion(a), true, nil
	}
	return "", false, nil
}

func extractUvxTarget(args []string) (string, bool, error) {
	// uvx --from pkg tool  /  uvx tool  /  uvx tool@ver
	fromPkg := ""
	skipNext := false
	for i := range args {
		if skipNext {
			skipNext = false
			continue
		}
		a := strings.TrimSpace(args[i])
		if a == "" {
			continue
		}
		al := strings.ToLower(a)
		switch {
		case al == "--from" || al == "-f":
			if i+1 >= len(args) {
				return "", false, fmt.Errorf("uvx %s 缺少包名", a)
			}
			fromPkg = strings.TrimSpace(args[i+1])
			skipNext = true
			continue
		case strings.HasPrefix(al, "--from="):
			fromPkg = strings.TrimSpace(a[len("--from="):])
			continue
		case al == "--with" || al == "--with-editable" || al == "--with-requirements":
			return "", false, fmt.Errorf("严格安全模式禁止 uvx 使用 %s 附加依赖，请将依赖固化到目标包", a)
		case al == "--python" || al == "-p" || al == "--index-url" || al == "--extra-index-url" ||
			al == "--cache-dir" || al == "--directory" || al == "--project":
			skipNext = true
			continue
		case al == "--isolated" || al == "--no-cache" || al == "--refresh" || al == "--all" ||
			al == "-q" || al == "--quiet" || al == "-v" || al == "--verbose":
			continue
		case al == "run" || al == "tool" || al == "install" || al == "pip":
			// 子命令形态：更偏向装包/脚本，严格档拒绝
			return "", false, fmt.Errorf("严格安全模式禁止 uvx 使用 %q 子命令，请直接写「uvx <工具名>」", a)
		case strings.HasPrefix(a, "-"):
			return "", false, fmt.Errorf("严格安全模式不支持的 uvx 参数 %q", a)
		default:
			// 位置参数：工具名
			spec := a
			if fromPkg != "" {
				if err := rejectDangerousPackageSpec(fromPkg); err != nil {
					return "", false, err
				}
				// 白名单按 --from 包名校验更贴近来源
				return stripPackageVersion(fromPkg), true, nil
			}
			if err := rejectDangerousPackageSpec(spec); err != nil {
				return "", false, err
			}
			return stripPackageVersion(spec), true, nil
		}
	}
	if fromPkg != "" {
		if err := rejectDangerousPackageSpec(fromPkg); err != nil {
			return "", false, err
		}
		return stripPackageVersion(fromPkg), true, nil
	}
	return "", false, nil
}

func rejectDangerousPackageSpec(spec string) error {
	s := strings.TrimSpace(spec)
	if s == "" {
		return fmt.Errorf("包/工具名不能为空")
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "://") || strings.HasPrefix(lower, "git+") || strings.Contains(lower, "github.com:") {
		return fmt.Errorf("严格安全模式禁止通过 URL/Git 引用包 %q", s)
	}
	// 本地路径
	if strings.HasPrefix(s, ".") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "\\") ||
		(len(s) > 2 && s[1] == ':' && (s[2] == '\\' || s[2] == '/')) {
		return fmt.Errorf("严格安全模式禁止 npx/uvx 执行本地路径 %q", s)
	}
	if strings.ContainsAny(s, " \t\n") {
		return fmt.Errorf("包/工具名含非法空白")
	}
	return nil
}

// stripPackageVersion 去掉末尾 @version（保留 scope 内的 @）。
// 例：@scope/pkg@1.2.3 → @scope/pkg；pkg@1.0.0 → pkg；ruff → ruff。
func stripPackageVersion(spec string) string {
	s := strings.TrimSpace(spec)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "@") {
		// @scope/name@version
		rest := s[1:]
		if i := strings.LastIndex(rest, "@"); i > 0 {
			// 确保 @ 出现在 scope/name 之后
			return "@" + rest[:i]
		}
		return s
	}
	if i := strings.LastIndex(s, "@"); i > 0 {
		return s[:i]
	}
	return s
}

func normalizePackageAllowlist(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		p := strings.ToLower(strings.TrimSpace(item))
		if p == "" || p == "*" {
			continue
		}
		// 统一去掉版本
		if !strings.HasSuffix(p, "/*") {
			p = strings.ToLower(stripPackageVersion(p))
		}
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

func packageAllowed(target string, allowlist []string) bool {
	t := strings.ToLower(strings.TrimSpace(stripPackageVersion(target)))
	if t == "" {
		return false
	}
	for _, a := range allowlist {
		if a == t {
			return true
		}
		// @scope/* 前缀：匹配 @scope/name，不匹配更深路径
		if strings.HasSuffix(a, "/*") {
			prefix := strings.TrimSuffix(a, "*") // e.g. "@modelcontextprotocol/"
			if rest, ok := strings.CutPrefix(t, prefix); ok {
				if rest != "" && !strings.Contains(rest, "/") {
					return true
				}
			}
		}
	}
	return false
}

// MergePackageAllowlist 合并全局与上游包白名单（并集）。
func MergePackageAllowlist(global, local []string) []string {
	return normalizePackageAllowlist(append(append([]string{}, global...), local...))
}
