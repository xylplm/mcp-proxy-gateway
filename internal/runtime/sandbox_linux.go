//go:build linux

package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	bwrapCache struct {
		sync.RWMutex
		path string
		at   time.Time
	}
)

const bwrapCacheTTL = 30 * time.Second

func lookPathBwrap() string {
	now := time.Now()
	bwrapCache.RLock()
	if now.Sub(bwrapCache.at) < bwrapCacheTTL {
		path := bwrapCache.path
		bwrapCache.RUnlock()
		return path
	}
	bwrapCache.RUnlock()

	bwrapCache.Lock()
	defer bwrapCache.Unlock()
	now = time.Now()
	if now.Sub(bwrapCache.at) < bwrapCacheTTL {
		return bwrapCache.path
	}
	path, _ := exec.LookPath("bwrap")
	bwrapCache.path = path
	bwrapCache.at = now
	return path
}

func applySandboxPlatform(cmd *exec.Cmd, opts SandboxOptions) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// 独立进程组：便于会话关闭时按组清理；不改变 SDK Close 语义。
	cmd.SysProcAttr.Setpgid = true
	// 父进程（网关）异常退出时终止子进程，降低孤儿 MCP 进程残留。
	cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM

	// 严格档 + 可用 bwrap：真正包装命令实现文件/网络隔离。
	if opts.SecurityMode == SecurityModeStrict {
		if path := lookPathBwrap(); path != "" {
			wrapCommandWithBwrap(cmd, path, opts)
		}
	}
}

func describeSandboxPlatform() SandboxCapabilities {
	caps := SandboxCapabilities{
		ProcessHardeningSupported:    true,
		FilesystemIsolationSupported: false,
		NetworkIsolationSupported:    false,
		HostAllowlistEnforced:        false,
		IsolationBackend:             "none",
		Platform:                     "linux",
		Description:                  "Linux：stdio 子进程使用独立进程组，并在网关进程退出时发送 SIGTERM。文件/网络默认为策略约束。",
	}
	if path := lookPathBwrap(); path != "" {
		caps.FilesystemIsolationSupported = true
		caps.NetworkIsolationSupported = true
		caps.IsolationBackend = "bwrap"
		caps.Description = "Linux：进程组加固可用；检测到 bubblewrap，严格档将启用文件 bind 隔离，网络 deny 时使用网络命名空间断网。主机 allowlist 仍为策略声明（无特权无法内核过滤域名）。"
	}
	return caps
}

// wrapCommandWithBwrap 把 cmd 改写为 bwrap -- <原命令>。
func wrapCommandWithBwrap(cmd *exec.Cmd, bwrap string, opts SandboxOptions) {
	if cmd == nil || bwrap == "" || cmd.Path == "" {
		return
	}
	// 已包装则跳过。
	if filepath.Base(cmd.Path) == "bwrap" || strings.HasSuffix(cmd.Path, "/bwrap") {
		return
	}

	args := make([]string, 0, 80+len(cmd.Args))
	args = append(args,
		"--die-with-parent",
		"--new-session",
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--tmpfs", "/var/tmp",
		"--tmpfs", "/run",
	)

	// 只读系统树：优先挂 /usr，再按存在性补 lib/bin（避免与 symlink 冲突）。
	roBindIfDir := func(host string) {
		if st, err := os.Stat(host); err == nil && st.IsDir() {
			args = append(args, "--ro-bind", host, host)
		}
	}
	roBindIfDir("/usr")
	// 兼容非 usrmerge 发行版：直接绑定独立目录。
	for _, p := range []string{"/bin", "/sbin", "/lib", "/lib64", "/lib32", "/etc", "/opt"} {
		// 若已是指向 /usr 的符号链接，跳过，后面用 --symlink 覆盖语义。
		if info, err := os.Lstat(p); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if info.IsDir() {
				args = append(args, "--ro-bind", p, p)
			}
		}
	}
	// usrmerge：把缺失的顶层目录链接到 usr 下（仅当路径不存在时）。
	for _, link := range []struct{ name, target string }{
		{"/bin", "usr/bin"},
		{"/sbin", "usr/sbin"},
		{"/lib", "usr/lib"},
		{"/lib64", "usr/lib64"},
	} {
		if _, err := os.Lstat(link.name); os.IsNotExist(err) {
			args = append(args, "--symlink", link.target, link.name)
		}
	}

	// 运行时卷只读挂载：npm/pip 共享依赖与用户放入 bin 的可执行文件。
	// 镜像内置解释器（/opt/node、/usr/local）已由上面的 /usr、/opt 只读挂载覆盖。
	if opts.RuntimeDir != "" {
		if st, err := os.Stat(opts.RuntimeDir); err == nil && st.IsDir() {
			args = append(args, "--ro-bind", opts.RuntimeDir, opts.RuntimeDir)
		}
	}

	// 可写文件根：cwd + FileRoots。
	bound := map[string]struct{}{}
	bindRW := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		clean := filepath.Clean(p)
		if _, ok := bound[clean]; ok {
			return
		}
		st, err := os.Stat(clean)
		if err != nil {
			return
		}
		if !st.IsDir() {
			clean = filepath.Dir(clean)
			if _, ok := bound[clean]; ok {
				return
			}
			if st2, err2 := os.Stat(clean); err2 != nil || !st2.IsDir() {
				return
			}
		}
		bound[clean] = struct{}{}
		args = append(args, "--bind", clean, clean)
	}
	for _, root := range opts.FileRoots {
		bindRW(root)
	}
	if opts.CWD != "" {
		bindRW(opts.CWD)
	}
	if len(bound) == 0 {
		args = append(args, "--dir", "/home/mcp", "--chdir", "/home/mcp")
	}

	// 网络：deny 真正断网；allowlist 无特权无法按主机过滤，保持共享网络 + 策略声明。
	if opts.NetworkMode == NetworkAccessDeny {
		args = append(args, "--unshare-net")
	}

	if opts.CWD != "" {
		if st, err := os.Stat(opts.CWD); err == nil && st.IsDir() {
			args = append(args, "--chdir", opts.CWD)
		}
	}

	origPath := cmd.Path
	origArgs := make([]string, 0, len(cmd.Args))
	if len(cmd.Args) > 0 {
		origArgs = append(origArgs, cmd.Args[1:]...)
	}
	args = append(args, "--", origPath)
	args = append(args, origArgs...)

	cmd.Path = bwrap
	cmd.Args = append([]string{bwrap}, args...)
}
