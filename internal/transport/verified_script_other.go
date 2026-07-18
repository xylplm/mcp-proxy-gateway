//go:build !unix

package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func prepareVerifiedScript(path, expectedHash string) (*verifiedScriptLaunch, error) {
	source, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("脚本入口不可读")
	}
	defer source.Close()
	content, err := io.ReadAll(io.LimitReader(source, 1<<20+1))
	if err != nil || len(content) > 1<<20 {
		return nil, fmt.Errorf("脚本入口不可读或超过大小限制")
	}
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != expectedHash {
		return nil, fmt.Errorf("脚本版本内容已变化，哈希校验失败，请重新选择脚本版本")
	}
	dir, err := os.MkdirTemp("", "mpg-script-*")
	if err != nil {
		return nil, fmt.Errorf("无法创建脚本执行快照")
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	snapshot := filepath.Join(dir, filepath.Base(path))
	if err := os.WriteFile(snapshot, content, 0o600); err != nil {
		cleanup()
		return nil, fmt.Errorf("无法创建脚本执行快照")
	}
	return &verifiedScriptLaunch{Path: snapshot, cleanup: cleanup}, nil
}
