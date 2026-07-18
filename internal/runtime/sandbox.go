package runtime

import "os/exec"

// SandboxOptions 控制 stdio 子进程加固行为。
type SandboxOptions struct {
	// Enabled 为 false 时不修改 cmd。
	Enabled bool
	// SecurityMode 用于能力描述与后续隔离后端选择。
	SecurityMode StdioSecurityMode
	// FileRoots 预留给 Phase C 真隔离 bind-mount；Phase A 不改 cmd 启动方式。
	FileRoots []string
	// NetworkMode 预留给 Phase C 网络命名空间。
	NetworkMode NetworkAccessMode
}

// ApplySandbox 对 stdio 子进程应用平台可用的进程加固。
//
// 不改变 Command / Args / Env / Dir；不绑定 dial context。
// Linux：独立进程组 + 父进程死亡时发送 SIGTERM。
// 其他平台：安全 no-op，保证 Windows 开发/测试路径零影响。
// Phase C 可在此包装 bwrap；当前仅进程级加固。
func ApplySandbox(cmd *exec.Cmd, opts SandboxOptions) {
	if cmd == nil || !opts.Enabled {
		return
	}
	applySandboxPlatform(cmd)
}

// SandboxCapabilities 描述当前构建目标上的加固能力（给管理台展示）。
type SandboxCapabilities struct {
	ProcessHardeningSupported    bool   `json:"processHardeningSupported"`
	FilesystemIsolationSupported bool   `json:"filesystemIsolationSupported"`
	NetworkIsolationSupported    bool   `json:"networkIsolationSupported"`
	IsolationBackend             string `json:"isolationBackend,omitempty"` // none | bwrap | ...
	Platform                     string `json:"platform"`
	Description                  string `json:"description"`
}

// DescribeSandbox 返回当前平台沙箱能力说明。
func DescribeSandbox() SandboxCapabilities {
	caps := describeSandboxPlatform()
	// Phase C：探测 bwrap；此处统一补齐字段默认值。
	if caps.IsolationBackend == "" {
		caps.IsolationBackend = "none"
	}
	return caps
}
