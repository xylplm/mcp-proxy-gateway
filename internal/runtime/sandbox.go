package runtime

import "os/exec"

// SandboxOptions 控制 stdio 子进程加固行为。
type SandboxOptions struct {
	// Enabled 为 false 时不修改 cmd。
	Enabled bool
}

// ApplySandbox 对 stdio 子进程应用平台可用的进程加固。
//
// 不改变 Command / Args / Env / Dir；不绑定 dial context。
// Linux：独立进程组 + 父进程死亡时发送 SIGTERM。
// 其他平台：安全 no-op，保证 Windows 开发/测试路径零影响。
func ApplySandbox(cmd *exec.Cmd, opts SandboxOptions) {
	if cmd == nil || !opts.Enabled {
		return
	}
	applySandboxPlatform(cmd)
}

// SandboxCapabilities 描述当前构建目标上的加固能力（给管理台展示）。
type SandboxCapabilities struct {
	ProcessHardeningSupported bool   `json:"processHardeningSupported"`
	Platform                  string `json:"platform"`
	Description               string `json:"description"`
}

// DescribeSandbox 返回当前平台沙箱能力说明。
func DescribeSandbox() SandboxCapabilities {
	return describeSandboxPlatform()
}
