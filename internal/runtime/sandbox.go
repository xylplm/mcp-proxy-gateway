package runtime

import (
	"os/exec"
	"strings"
)

// SandboxOptions 控制 stdio 子进程加固与可选真隔离。
type SandboxOptions struct {
	// Enabled 为 false 时不修改 cmd。
	Enabled bool
	// SecurityMode 用于能力描述与隔离后端选择。
	SecurityMode StdioSecurityMode
	// FileRoots 为严格档可写文件根；存在且后端可用时会 bind 进沙箱。
	FileRoots []string
	// CWD 为子进程工作目录；沙箱启用时会 chdir 到该路径。
	CWD string
	// NetworkMode 为出站网络策略；deny 时在支持的后端上 unshare-net。
	NetworkMode NetworkAccessMode
	// NetworkHosts 为 allowlist 主机声明（协作信号；非内核按主机过滤）。
	NetworkHosts []string
	// RuntimeDir 为卷内 runtime，只读挂载以便解析 node/python。
	RuntimeDir string
}

// ApplySandbox 对 stdio 子进程应用平台可用的进程加固与真隔离。
//
// Linux：
//   - 始终：独立进程组 + 父死亡信号；
//   - 严格档且检测到 bubblewrap：包装 bwrap，按 FileRoots 绑定文件系统，
//     NetworkMode=deny 时 --unshare-net。
//
// 其他平台：尽量保持 no-op 或仅进程组，避免影响 Windows 开发路径。
func ApplySandbox(cmd *exec.Cmd, opts SandboxOptions) {
	if cmd == nil || !opts.Enabled {
		return
	}
	opts.FileRoots = normalizePathList(opts.FileRoots)
	opts.NetworkHosts = normalizeHostList(opts.NetworkHosts)
	opts.CWD = strings.TrimSpace(opts.CWD)
	opts.RuntimeDir = strings.TrimSpace(opts.RuntimeDir)
	opts.SecurityMode = NormalizeSecurityMode(string(opts.SecurityMode), SecurityModeStandard)
	opts.NetworkMode = NetworkAccessMode(strings.ToLower(strings.TrimSpace(string(opts.NetworkMode))))
	applySandboxPlatform(cmd, opts)
}

// SandboxCapabilities 描述当前构建目标上的加固能力（给管理台展示）。
type SandboxCapabilities struct {
	ProcessHardeningSupported    bool   `json:"processHardeningSupported"`
	FilesystemIsolationSupported bool   `json:"filesystemIsolationSupported"`
	NetworkIsolationSupported    bool   `json:"networkIsolationSupported"`
	HostAllowlistEnforced        bool   `json:"hostAllowlistEnforced"`      // 是否可内核级按主机过滤
	IsolationBackend             string `json:"isolationBackend,omitempty"` // none | bwrap | job | ...
	Platform                     string `json:"platform"`
	Description                  string `json:"description"`
}

// DescribeSandbox 返回当前平台沙箱能力说明。
func DescribeSandbox() SandboxCapabilities {
	caps := describeSandboxPlatform()
	if caps.IsolationBackend == "" {
		caps.IsolationBackend = "none"
	}
	return caps
}

// IsolationAvailable 报告当前进程是否能对严格档启用文件/网络真隔离。
func IsolationAvailable() bool {
	caps := DescribeSandbox()
	return caps.FilesystemIsolationSupported || caps.NetworkIsolationSupported
}
