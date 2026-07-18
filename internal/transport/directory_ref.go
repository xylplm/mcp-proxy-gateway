package transport

import (
	"fmt"
	"strings"

	"github.com/myGithub/mcp-proxy-gateway/internal/runtime"
)

type directoryRef struct {
	Root    string
	EntryID string
}

func directoryRefFromParams(params map[string]any) (directoryRef, bool, error) {
	mode, _ := params[ParamLaunchMode].(string)
	if strings.TrimSpace(mode) != "directory" {
		return directoryRef{}, false, nil
	}
	raw, ok := params["directoryRef"]
	if !ok || raw == nil {
		return directoryRef{}, true, fmt.Errorf("目录启动缺少 directoryRef")
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return directoryRef{}, true, fmt.Errorf("directoryRef 格式非法")
	}
	root, _ := m["root"].(string)
	entryID, _ := m["entryId"].(string)
	root = strings.TrimSpace(root)
	entryID = strings.TrimSpace(entryID)
	if root == "" || entryID == "" {
		return directoryRef{}, true, fmt.Errorf("directoryRef.root/entryId 不能为空")
	}
	return directoryRef{Root: root, EntryID: entryID}, true, nil
}

// resolveDirectoryLaunch fail-closed 重新扫描目录清单，忽略客户端夹带的 command/args/cwd。
func resolveDirectoryLaunch(params map[string]any, policy runtime.Policy, declaredRoots []string) (command string, args []string, cwd string, ok bool, err error) {
	ref, isDirectory, err := directoryRefFromParams(params)
	if err != nil || !isDirectory {
		return "", nil, "", isDirectory, err
	}
	// 额外浏览根仅供管理台选路，不授予代码执行权限。
	roots := append([]string{}, policy.GlobalFileRoots...)
	roots = append(roots, declaredRoots...)
	if len(roots) == 0 {
		return "", nil, "", true, fmt.Errorf("目录启动根不在文件允许路径内")
	}
	resolvedRoot, allowed, resolveErr := runtime.ResolveExistingPathWithinRoots(ref.Root, roots)
	if resolveErr != nil || !allowed {
		return "", nil, "", true, fmt.Errorf("目录启动根真实位置不在允许路径内")
	}
	entry, err := runtime.ResolveDirectoryLaunchEntry(resolvedRoot, ref.EntryID, policy)
	if err != nil {
		return "", nil, "", true, err
	}
	return entry.Command, entry.Args, entry.CWD, true, nil
}
