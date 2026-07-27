package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const DirectoryLaunchManifest = "mpg.launch.json"

// DirectoryLaunchEntry 为目录启动清单单个入口。
type DirectoryLaunchEntry struct {
	ID              string   `json:"id"`
	Label           string   `json:"label,omitempty"`
	Runtime         string   `json:"runtime"`
	Command         string   `json:"command"`
	Args            []string `json:"args"`
	CWD             string   `json:"cwd,omitempty"`
	RecommendedMode string   `json:"recommendedMode,omitempty"`
}

type directoryLaunchManifest struct {
	Version int                    `json:"version"`
	Entries []DirectoryLaunchEntry `json:"entries"`
}

// DirectoryLaunchResult 为目录探测结果。
type DirectoryLaunchResult struct {
	Root         string                 `json:"root"`
	ManifestPath string                 `json:"manifestPath,omitempty"`
	Entries      []DirectoryLaunchEntry `json:"entries"`
	Warnings     []string               `json:"warnings"`
}

// ResolveDirectoryLaunchEntry 重新扫描目录并按 entryID 解析入口，供运行时 fail-closed 使用。
func ResolveDirectoryLaunchEntry(root, entryID string, policy Policy) (DirectoryLaunchEntry, error) {
	result, err := InspectDirectoryLaunch(root, policy)
	if err != nil {
		return DirectoryLaunchEntry{}, err
	}
	for _, entry := range result.Entries {
		if entry.ID == strings.TrimSpace(entryID) {
			return entry, nil
		}
	}
	return DirectoryLaunchEntry{}, fmt.Errorf("目录启动入口不存在：%s", entryID)
}

// InspectDirectoryLaunch 只读扫描本地目录入口，不执行任何代码。
//
// 优先读取 mpg.launch.json；否则识别 package.json/main.py/server.py/index.js。
// 所有入口路径必须保持在 root 内，command 必须是白名单解释器基名。
func InspectDirectoryLaunch(root string, policy Policy) (DirectoryLaunchResult, error) {
	clean, err := NormalizeBrowsePath(root)
	if err != nil {
		return DirectoryLaunchResult{}, err
	}
	st, err := os.Stat(clean)
	if err != nil || !st.IsDir() {
		return DirectoryLaunchResult{}, fmt.Errorf("目录不存在或不可访问")
	}
	resolvedRoot, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return DirectoryLaunchResult{}, fmt.Errorf("目录真实路径无法解析")
	}
	resolvedRoot, err = NormalizeBrowsePath(resolvedRoot)
	if err != nil {
		return DirectoryLaunchResult{}, fmt.Errorf("目录真实路径非法")
	}
	clean = resolvedRoot
	res := DirectoryLaunchResult{Root: clean, Entries: []DirectoryLaunchEntry{}, Warnings: []string{}}
	manifestPath := filepath.Join(clean, DirectoryLaunchManifest)
	if b, err := readLimitedFile(manifestPath, 256*1024); err == nil {
		var manifest directoryLaunchManifest
		if err := json.Unmarshal(b, &manifest); err != nil {
			return DirectoryLaunchResult{}, fmt.Errorf("mpg.launch.json 格式非法")
		}
		if manifest.Version != 1 || len(manifest.Entries) == 0 || len(manifest.Entries) > 16 {
			return DirectoryLaunchResult{}, fmt.Errorf("mpg.launch.json 版本或 entries 非法")
		}
		seen := map[string]struct{}{}
		for i, entry := range manifest.Entries {
			normalized, err := normalizeDirectoryEntry(clean, entry, policy)
			if err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("entries[%d] 已跳过：%v", i, err))
				continue
			}
			if _, ok := seen[normalized.ID]; ok {
				res.Warnings = append(res.Warnings, fmt.Sprintf("entries[%d] 入口 ID 重复已跳过：%s", i, normalized.ID))
				continue
			}
			seen[normalized.ID] = struct{}{}
			res.Entries = append(res.Entries, normalized)
		}
		if len(res.Entries) == 0 {
			return DirectoryLaunchResult{}, fmt.Errorf("mpg.launch.json 中没有可用入口：%s", strings.Join(res.Warnings, "；"))
		}
		res.ManifestPath = manifestPath
		return res, nil
	} else if !os.IsNotExist(err) {
		return DirectoryLaunchResult{}, err
	}

	// 约定探测：仅添加实际存在文件。
	candidates := []DirectoryLaunchEntry{
		{ID: "python-main", Label: "Python main.py", Runtime: "python3", Command: "python3", Args: []string{"main.py"}, CWD: ".", RecommendedMode: "strict"},
		{ID: "python-server", Label: "Python server.py", Runtime: "python3", Command: "python3", Args: []string{"server.py"}, CWD: ".", RecommendedMode: "strict"},
		{ID: "node-index", Label: "Node index.js", Runtime: "node", Command: "node", Args: []string{"index.js"}, CWD: ".", RecommendedMode: "strict"},
		{ID: "node-dist", Label: "Node dist/index.js", Runtime: "node", Command: "node", Args: []string{"dist/index.js"}, CWD: ".", RecommendedMode: "strict"},
	}
	for _, entry := range candidates {
		path := filepath.Join(clean, filepath.FromSlash(entry.Args[0]))
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			normalized, nerr := normalizeDirectoryEntry(clean, entry, policy)
			if nerr == nil {
				res.Entries = append(res.Entries, normalized)
			}
		}
	}
	if len(res.Entries) == 0 {
		return DirectoryLaunchResult{}, fmt.Errorf("未识别到可启动入口，请在目录中添加 mpg.launch.json、main.py、server.py 或 index.js")
	}
	res.Warnings = append(res.Warnings, "使用约定入口；建议添加 mpg.launch.json 固定启动参数")
	return res, nil
}

func readLimitedFile(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, fmt.Errorf("读取启动清单失败")
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("启动清单过大")
	}
	return b, nil
}

func normalizeDirectoryEntry(root string, entry DirectoryLaunchEntry, policy Policy) (DirectoryLaunchEntry, error) {
	entry.ID = strings.TrimSpace(entry.ID)
	entry.Command = strings.TrimSpace(entry.Command)
	entry.Runtime = strings.TrimSpace(entry.Runtime)
	entry.CWD = strings.TrimSpace(entry.CWD)
	if entry.ID == "" || len(entry.ID) > 64 || strings.ContainsAny(entry.ID, `/\`) {
		return DirectoryLaunchEntry{}, fmt.Errorf("入口 ID 非法")
	}
	if entry.Command == "" {
		entry.Command = entry.Runtime
	}
	if entry.Runtime == "" {
		entry.Runtime = CommandBaseName(entry.Command)
	}
	if err := ValidateCommand(entry.Command, policy); err != nil {
		return DirectoryLaunchEntry{}, err
	}
	if len(entry.Args) == 0 || len(entry.Args) > 128 {
		return DirectoryLaunchEntry{}, fmt.Errorf("args 不能为空且最多 128 项")
	}
	for i, arg := range entry.Args {
		if strings.ContainsRune(arg, 0) || len(arg) > 2048 {
			return DirectoryLaunchEntry{}, fmt.Errorf("args[%d] 非法", i)
		}
	}
	// 首个参数通常为入口脚本；若为相对路径，必须留在 root。
	first := strings.TrimSpace(entry.Args[0])
	if first == "" || strings.HasPrefix(first, "-") {
		return DirectoryLaunchEntry{}, fmt.Errorf("args[0] 必须是项目目录内的入口脚本路径")
	}
	var target string
	if filepath.IsAbs(first) || strings.HasPrefix(first, "/") {
		target = filepath.Clean(first)
	} else {
		target = filepath.Join(root, filepath.FromSlash(first))
	}
	resolvedTarget, allowed, err := ResolveExistingPathWithinRoots(target, []string{root})
	if err != nil || !allowed {
		return DirectoryLaunchEntry{}, fmt.Errorf("入口文件越出项目目录或无法解析")
	}
	if st, err := os.Stat(resolvedTarget); err != nil || st.IsDir() {
		return DirectoryLaunchEntry{}, fmt.Errorf("入口文件不存在：%s", first)
	}
	entry.Args[0] = resolvedTarget
	cwd := root
	if entry.CWD != "" && entry.CWD != "." {
		if filepath.IsAbs(entry.CWD) || strings.HasPrefix(entry.CWD, "/") {
			cwd = filepath.Clean(entry.CWD)
		} else {
			cwd = filepath.Join(root, filepath.FromSlash(entry.CWD))
		}
	}
	resolvedCWD, allowed, err := ResolveExistingPathWithinRoots(cwd, []string{root})
	if err != nil || !allowed {
		return DirectoryLaunchEntry{}, fmt.Errorf("cwd 越出项目目录或无法解析")
	}
	if st, err := os.Stat(resolvedCWD); err != nil || !st.IsDir() {
		return DirectoryLaunchEntry{}, fmt.Errorf("cwd 不存在或不是目录")
	}
	entry.CWD = resolvedCWD
	if entry.Label == "" {
		entry.Label = entry.ID
	}
	if entry.RecommendedMode == "" {
		entry.RecommendedMode = "strict"
	}
	return entry, nil
}
