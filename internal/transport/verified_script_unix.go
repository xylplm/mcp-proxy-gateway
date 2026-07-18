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
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("脚本入口不可读")
	}
	h := sha256.New()
	// 与 scripts.MaxScriptBytes 对齐；多读 1 字节用于拒绝被篡改放大的文件。
	n, err := io.Copy(h, io.LimitReader(f, 1<<20+1))
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("脚本入口不可读")
	}
	if n > 1<<20 {
		_ = f.Close()
		return nil, fmt.Errorf("脚本入口不可读或超过大小限制")
	}
	if hex.EncodeToString(h.Sum(nil)) != expectedHash {
		_ = f.Close()
		return nil, fmt.Errorf("脚本版本内容已变化，哈希校验失败，请重新选择脚本版本")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("脚本入口不可读")
	}
	fdPath := "/proc/self/fd/3"
	if runtime.GOOS == "darwin" {
		fdPath = "/dev/fd/3"
	}
	return &verifiedScriptLaunch{
		Path:       fdPath,
		ExtraFiles: []*os.File{f},
		cleanup:    func() { _ = f.Close() },
	}, nil
}
