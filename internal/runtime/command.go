package runtime

import (
	"fmt"
	"strings"
)

// 始终拒绝作为 stdio command 的危险解释器 / shell / 包装器（不可通过 allowlist 放开）。
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
	// 包装器：可把任意命令当参数执行，绕过「command 白名单」。
	"env":     {},
	"nice":    {},
	"nohup":   {},
	"xargs":   {},
	"time":    {},
	"timeout": {},
	"stdbuf":  {},
	"busybox": {},
	"perl":    {},
	"ruby":    {},
	"lua":     {},
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
