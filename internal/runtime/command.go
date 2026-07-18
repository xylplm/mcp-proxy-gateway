package runtime

import (
	"fmt"
	"os/exec"
	"strings"
)

// 始终拒绝作为 stdio command 的危险解释器 / shell（不可通过 allowlist 放开）。
var deniedCommandBases = map[string]struct{}{
	"sh":             {},
	"bash":           {},
	"zsh":            {},
	"fish":           {},
	"dash":           {},
	"csh":            {},
	"tcsh":           {},
	"ksh":            {},
	"cmd":            {},
	"cmd.exe":        {},
	"powershell":     {},
	"powershell.exe": {},
	"pwsh":           {},
	"pwsh.exe":       {},
	"wscript":        {},
	"wscript.exe":    {},
	"cscript":        {},
	"cscript.exe":    {},
	"mshta":          {},
	"mshta.exe":      {},
	"sudo":           {},
	"su":             {},
	"doas":           {},
}

// ValidateCommand 校验 stdio 启动命令是否允许。
//
// command 可为逻辑名或绝对/相对路径；按基名做 denylist / allowlist 判断。
// 返回的错误文案面向管理台用户，适合直接作为字段级校验说明。
func ValidateCommand(command string, policy Policy) error {
	policy = NormalizePolicy(policy)
	if !policy.StdioEnabled {
		return fmt.Errorf("本地 stdio 上游已禁用（runtime.stdio_enabled=false）")
	}

	raw := strings.TrimSpace(command)
	if raw == "" {
		return fmt.Errorf("连接参数 \"command\" 不能为空")
	}

	base := CommandBaseName(raw)
	if base == "" {
		return fmt.Errorf("连接参数 \"command\" 无效")
	}

	if _, denied := deniedCommandBases[base]; denied {
		return fmt.Errorf("出于安全原因禁止以 shell/脚本宿主 %q 作为 stdio 启动命令", base)
	}
	// 兼容带 .exe 的 denylist 键（CommandBaseName 已剥扩展，此处再防直接匹配）。
	if _, denied := deniedCommandBases[base+".exe"]; denied {
		return fmt.Errorf("出于安全原因禁止以 shell/脚本宿主 %q 作为 stdio 启动命令", base)
	}

	if len(policy.CommandAllowlist) == 0 {
		return nil
	}
	for _, allowed := range policy.CommandAllowlist {
		if base == allowed {
			return nil
		}
	}
	return fmt.Errorf(
		"命令 %q 不在 stdio 允许列表中，请在系统设置或运行环境中调整策略",
		base,
	)
}

// ResolveCommand 将 command 解析为可执行路径。
//
// 若 command 已是绝对路径或包含路径分隔符，优先原样交由 LookPath 处理；
// 找不到时返回面向用户的错误（引导查看运行环境页）。
func ResolveCommand(command string) (string, error) {
	raw := strings.TrimSpace(command)
	if raw == "" {
		return "", fmt.Errorf("连接参数 \"command\" 不能为空")
	}
	resolved, err := exec.LookPath(raw)
	if err != nil {
		base := CommandBaseName(raw)
		if base == "" {
			base = raw
		}
		return "", fmt.Errorf(
			"未找到可执行文件 %q。当前环境缺少该工具，请安装对应运行时或改用远程 MCP。可在「运行环境」查看探测结果",
			base,
		)
	}
	return resolved, nil
}
