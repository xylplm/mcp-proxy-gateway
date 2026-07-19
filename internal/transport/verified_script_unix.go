//go:build unix

package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"
)

func prepareVerifiedScript(path, expectedHash string) (*verifiedScriptLaunch, error) {
	source, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("脚本入口不可读")
	}
	// 与 scripts.MaxScriptBytes 对齐；多读 1 字节用于拒绝被篡改放大的文件。
	content, err := io.ReadAll(io.LimitReader(source, 1<<20+1))
	_ = source.Close()
	if err != nil || len(content) > 1<<20 {
		return nil, fmt.Errorf("脚本入口不可读或超过大小限制")
	}
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != expectedHash {
		return nil, fmt.Errorf("脚本版本内容已变化，哈希校验失败，请重新选择脚本版本")
	}

	// 将已校验字节钉到私有临时文件，再通过 ExtraFiles 继承给子进程。
	// 直接复用原路径 FD 无法抵御 O_TRUNC 原地覆写；快照路径在 bwrap 的
	// 私有 tmpfs 下也可能不可见，因此走 /proc/self/fd（Linux）或 /dev/fd（Darwin）。
	tmp, err := os.CreateTemp("", "mpg-script-*")
	if err != nil {
		return nil, fmt.Errorf("无法创建脚本执行快照")
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(content); err != nil {
		cleanup()
		return nil, fmt.Errorf("无法创建脚本执行快照")
	}
	_ = tmp.Chmod(0o600)
	// 尽量 unlink 路径，仅保留打开的 FD，避免后续经路径改写快照内容。
	if err := os.Remove(tmpName); err == nil {
		cleanup = func() { _ = tmp.Close() }
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, fmt.Errorf("无法创建脚本执行快照")
	}

	fdPath := "/proc/self/fd/3"
	if runtime.GOOS == "darwin" {
		fdPath = "/dev/fd/3"
	}
	return &verifiedScriptLaunch{
		Path:       fdPath,
		ExtraFiles: []*os.File{tmp},
		cleanup:    cleanup,
	}, nil
}
