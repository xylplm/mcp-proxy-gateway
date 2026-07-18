package runtime

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// 路径浏览模式：决定 list 返回目录、文件或两者。
const (
	BrowseModeDirectory = "directory"
	BrowseModeFile      = "file"
	BrowseModeAny       = "any"
)

const (
	browseDefaultLimit = 200
	browseMaxLimit     = 500
	browseMaxRoots     = 32
	browseMaxContext   = 16
)

// BrowseRoot 为路径选择器侧栏入口。
type BrowseRoot struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Path  string `json:"path"`
	Kind  string `json:"kind"` // data | runtime | global_file | context
}

// BrowseEntry 为单层目录项。
type BrowseEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Type      string `json:"type"` // dir | file
	Size      int64  `json:"size,omitempty"`
	ModTime   string `json:"modTime,omitempty"`
	Readable  bool   `json:"readable"`
	Enterable bool   `json:"enterable"`
}

// BrowseRootsResult 为可选根目录响应。
type BrowseRootsResult struct {
	Roots         []BrowseRoot `json:"roots"`
	Platform      string       `json:"platform"`
	PathSeparator string       `json:"pathSeparator"`
	HostHint      string       `json:"hostHint,omitempty"`
}

// BrowseListResult 为单层列举结果。
type BrowseListResult struct {
	Path          string        `json:"path"`
	Parent        string        `json:"parent,omitempty"`
	Entries       []BrowseEntry `json:"entries"`
	Truncated     bool          `json:"truncated"`
	Platform      string        `json:"platform"`
	PathSeparator string        `json:"pathSeparator"`
}

// BrowseStatResult 为路径状态探测结果。
type BrowseStatResult struct {
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Type     string `json:"type"` // dir | file | missing | other
	Allowed  bool   `json:"allowed"`
	Readable bool   `json:"readable"`
}

// NormalizeBrowsePath 规范化浏览路径：去空白、Clean、拒绝空与相对逃逸形态。
//
// 相对路径非法。Unix 风格绝对路径（以 / 开头）在 Windows 上保留 / 前缀语义，
// 便于容器路径、跨平台测试与路径前缀匹配。
func NormalizeBrowsePath(path string) (string, error) {
	raw := strings.TrimSpace(path)
	if raw == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	if strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("路径包含非法字符")
	}
	unixStyle := strings.HasPrefix(raw, "/")
	var normalized string
	if unixStyle {
		// 用正斜杠逻辑 Clean，避免 Windows 把 /data 变成相对路径 \data。
		normalized = filepath.ToSlash(filepath.Clean(filepath.FromSlash(raw)))
		if !strings.HasPrefix(normalized, "/") {
			normalized = "/" + normalized
		}
	} else {
		normalized = filepath.Clean(filepath.FromSlash(raw))
	}
	if normalized == "." || normalized == "" {
		return "", fmt.Errorf("路径非法")
	}
	if !isAbsPath(raw) && !isAbsPath(normalized) {
		return "", fmt.Errorf("请使用绝对路径")
	}
	return normalized, nil
}

// PathUnderAnyRoot 判断 path 是否在任一允许根的词法范围内（含根自身）。
//
// 对实际文件系统访问必须再调用 ResolveExistingPathWithinRoots，防止中间符号链接越界。
func PathUnderAnyRoot(path string, roots []string) bool {
	clean, err := NormalizeBrowsePath(path)
	if err != nil {
		return false
	}
	for _, root := range roots {
		if pathUnderRoot(clean, root) {
			return true
		}
	}
	return false
}

// ResolveExistingPathWithinRoots 解析 path 与 roots 的全部符号链接，并确认真实路径仍位于
// 某个真实根内。path 必须存在；返回解析后的真实路径。
func ResolveExistingPathWithinRoots(path string, roots []string) (string, bool, error) {
	clean, err := NormalizeBrowsePath(path)
	if err != nil {
		return "", false, err
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return clean, false, err
	}
	resolved, err = NormalizeBrowsePath(resolved)
	if err != nil {
		return clean, false, err
	}
	for _, root := range roots {
		rootClean, rerr := NormalizeBrowsePath(root)
		if rerr != nil {
			continue
		}
		rootResolved, rerr := filepath.EvalSymlinks(rootClean)
		if rerr != nil {
			continue
		}
		rootResolved, rerr = NormalizeBrowsePath(rootResolved)
		if rerr != nil {
			continue
		}
		// clean 可能是用户输入的词法路径，也可能已是上一步返回的真实路径。
		if (pathUnderRoot(clean, rootClean) || pathUnderRoot(clean, rootResolved)) && pathUnderRoot(resolved, rootResolved) {
			return resolved, true, nil
		}
	}
	return resolved, false, nil
}

// MissingPathWithinRoots 判断尚不存在的路径未来创建时是否仍位于受信根。
// 它解析最近存在祖先的符号链接；实际使用时仍需对最终存在路径重新校验。
func MissingPathWithinRoots(path string, roots []string) bool {
	clean, err := NormalizeBrowsePath(path)
	if err != nil || !PathUnderAnyRoot(clean, roots) {
		return false
	}
	ancestor := clean
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			_, allowed, rerr := ResolveExistingPathWithinRoots(ancestor, roots)
			return rerr == nil && allowed
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return false
		}
		ancestor = parent
	}
}

// BuildBrowseRoots 汇总可浏览根：数据目录、运行时目录、全局文件根、额外浏览根与表单上下文路径。
//
// contextRoots 仅作为「临时入口」；会去重、限制数量，且必须是绝对路径。
// browseExtraRoots 仅扩大管理台路径选择器范围，不改变 stdio 文件策略。
func BuildBrowseRoots(dataDir, runtimeDir string, globalFileRoots, browseExtraRoots, contextRoots []string) []BrowseRoot {
	out := make([]BrowseRoot, 0, 8)
	seen := map[string]struct{}{}

	add := func(id, label, kind, p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		clean, err := NormalizeBrowsePath(p)
		if err != nil {
			return
		}
		key := pathKey(clean)
		if _, ok := seen[key]; ok {
			return
		}
		// 根目录本身不存在时仍展示入口，便于用户发现配置位置。
		seen[key] = struct{}{}
		out = append(out, BrowseRoot{
			ID:    id,
			Label: label,
			Path:  clean,
			Kind:  kind,
		})
	}

	if d := strings.TrimSpace(dataDir); d != "" {
		add("data", "数据目录", "data", d)
	}
	if r := strings.TrimSpace(runtimeDir); r != "" {
		add("runtime", "运行时目录", "runtime", r)
	}
	for i, g := range globalFileRoots {
		if i >= browseMaxRoots {
			break
		}
		add(fmt.Sprintf("global_%d", i), "全局文件允许路径", "global_file", g)
	}
	for i, extra := range browseExtraRoots {
		if i >= browseMaxRoots {
			break
		}
		add(fmt.Sprintf("extra_%d", i), "额外浏览根", "extra", extra)
	}
	// contextRoots 只能在已有受信根内提供快捷入口，不能由客户端自行扩大浏览边界。
	trustedRoots := BrowseRootPaths(out)
	ctxCount := 0
	for _, c := range contextRoots {
		if ctxCount >= browseMaxContext {
			break
		}
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		clean, err := NormalizeBrowsePath(c)
		if err != nil || !PathUnderAnyRoot(clean, trustedRoots) {
			continue
		}
		resolved, allowed, resolveErr := ResolveExistingPathWithinRoots(clean, trustedRoots)
		if resolveErr == nil && allowed {
			if st, statErr := os.Stat(resolved); statErr == nil && !st.IsDir() {
				resolved = filepath.Dir(resolved)
			}
			c = resolved
		} else if !os.IsNotExist(resolveErr) || !MissingPathWithinRoots(clean, trustedRoots) {
			continue
		}
		add(fmt.Sprintf("context_%d", ctxCount), "当前表单路径", "context", c)
		ctxCount++
	}
	return out
}

// BrowseRootPaths 提取根路径列表。
func BrowseRootPaths(roots []BrowseRoot) []string {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		out = append(out, r.Path)
	}
	return out
}

// ClampBrowseLimit 夹紧列举上限。
func ClampBrowseLimit(limit int) int {
	if limit <= 0 {
		return browseDefaultLimit
	}
	if limit > browseMaxLimit {
		return browseMaxLimit
	}
	return limit
}

// NormalizeBrowseMode 归一化浏览模式。
func NormalizeBrowseMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case BrowseModeFile:
		return BrowseModeFile
	case BrowseModeAny:
		return BrowseModeAny
	default:
		return BrowseModeDirectory
	}
}

// ListBrowseDir 列举 path 下一层条目。path 必须落在 roots 内。
//
// 软链：不跟随进入；若软链目标可解析且仍在允许根内，目录软链可 enterable。
// 不递归；超限设置 Truncated。
func ListBrowseDir(path string, roots []string, mode string, limit int) (BrowseListResult, error) {
	mode = NormalizeBrowseMode(mode)
	limit = ClampBrowseLimit(limit)

	clean, err := NormalizeBrowsePath(path)
	if err != nil {
		return BrowseListResult{}, err
	}
	if !PathUnderAnyRoot(clean, roots) {
		return BrowseListResult{}, fmt.Errorf("路径不在允许浏览范围内")
	}
	resolved, allowed, resolveErr := ResolveExistingPathWithinRoots(clean, roots)
	if resolveErr != nil {
		if os.IsNotExist(resolveErr) {
			return BrowseListResult{}, fmt.Errorf("路径不存在")
		}
		return BrowseListResult{}, fmt.Errorf("无法解析路径")
	}
	if !allowed {
		return BrowseListResult{}, fmt.Errorf("路径真实位置不在允许浏览范围内")
	}
	clean = resolved

	st, err := os.Lstat(clean)
	if err != nil {
		if os.IsNotExist(err) {
			return BrowseListResult{}, fmt.Errorf("路径不存在")
		}
		return BrowseListResult{}, fmt.Errorf("无法访问路径")
	}
	if !st.IsDir() {
		// 软链目录：Lstat 可能不是 dir；若指向目录且在根内，允许打开。
		if st.Mode()&os.ModeSymlink != 0 {
			target, terr := filepath.EvalSymlinks(clean)
			if terr != nil {
				return BrowseListResult{}, fmt.Errorf("无法解析符号链接")
			}
			target, terr = NormalizeBrowsePath(target)
			if terr != nil || !PathUnderAnyRoot(target, roots) {
				return BrowseListResult{}, fmt.Errorf("符号链接目标不在允许范围内")
			}
			tst, terr := os.Stat(target)
			if terr != nil || !tst.IsDir() {
				return BrowseListResult{}, fmt.Errorf("路径不是目录")
			}
			clean = target
		} else {
			return BrowseListResult{}, fmt.Errorf("路径不是目录")
		}
	}

	f, err := os.Open(clean)
	if err != nil {
		if os.IsPermission(err) {
			return BrowseListResult{}, fmt.Errorf("没有读取权限")
		}
		return BrowseListResult{}, fmt.Errorf("无法打开目录")
	}
	defer f.Close()

	// 先按模式筛选再计算页面上限，避免大量文件把后面的目录永久截掉。
	// 分批读取，不把超大目录一次性加载进内存。
	entries := make([]BrowseEntry, 0, limit)
	truncated := false
	for len(entries) <= limit {
		names, readErr := f.Readdirnames(256)
		if readErr != nil && readErr != io.EOF {
			return BrowseListResult{}, fmt.Errorf("读取目录失败")
		}
		for _, name := range names {
			if name == "" || name == "." || name == ".." {
				continue
			}
			full := filepath.Join(clean, name)
			info, lerr := os.Lstat(full)
			if lerr != nil {
				// 竞态消失的条目跳过，不失败整次 list。
				continue
			}
			entryType, enterable, readable := classifyBrowseEntry(full, info, roots)
			if mode == BrowseModeDirectory && entryType != "dir" {
				continue
			}
			// 文件模式仍必须返回目录供逐级导航；文件项可选择，目录项仅进入。
			if mode == BrowseModeFile && entryType != "file" && entryType != "dir" {
				continue
			}
			item := BrowseEntry{
				Name:      name,
				Path:      full,
				Type:      entryType,
				Readable:  readable,
				Enterable: enterable,
			}
			if entryType == "file" {
				item.Size = info.Size()
			}
			if mt := info.ModTime(); !mt.IsZero() {
				item.ModTime = mt.UTC().Format(time.RFC3339)
			}
			entries = append(entries, item)
			if len(entries) > limit {
				truncated = true
				break
			}
		}
		if truncated || readErr == io.EOF || len(names) == 0 {
			break
		}
	}
	if truncated {
		entries = entries[:limit]
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Type != entries[j].Type {
			// 目录优先
			return entries[i].Type == "dir"
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	parent := browseParent(clean, roots)
	return BrowseListResult{
		Path:          clean,
		Parent:        parent,
		Entries:       entries,
		Truncated:     truncated,
		Platform:      runtime.GOOS,
		PathSeparator: string(filepath.Separator),
	}, nil
}

// StatBrowsePath 探测路径是否存在、类型与是否允许。
//
// 不存在时 Allowed 仍反映「若创建是否落在允许根内」；便于手输预填。
func StatBrowsePath(path string, roots []string) (BrowseStatResult, error) {
	clean, err := NormalizeBrowsePath(path)
	if err != nil {
		return BrowseStatResult{}, err
	}
	if !PathUnderAnyRoot(clean, roots) {
		return BrowseStatResult{}, fmt.Errorf("路径不在允许浏览范围内")
	}
	res := BrowseStatResult{
		Path:    clean,
		Allowed: true,
		Type:    "missing",
	}
	_, err = os.Lstat(clean)
	if err != nil {
		if os.IsNotExist(err) {
			res.Exists = false
			res.Allowed = MissingPathWithinRoots(clean, roots)
			return res, nil
		}
		if os.IsPermission(err) {
			res.Exists = true
			res.Type = "other"
			res.Readable = false
			return res, nil
		}
		return BrowseStatResult{}, fmt.Errorf("无法访问路径")
	}
	res.Exists = true
	resolved, realAllowed, resolveErr := ResolveExistingPathWithinRoots(clean, roots)
	if resolveErr != nil {
		res.Allowed = false
		res.Readable = false
		res.Type = "other"
		return res, nil
	}
	res.Path = resolved
	res.Allowed = realAllowed
	res.Readable = realAllowed
	if !realAllowed {
		res.Type = "other"
		return res, nil
	}
	st, err := os.Stat(resolved)
	if err != nil {
		res.Readable = false
		res.Type = "other"
		return res, nil
	}
	switch {
	case st.IsDir():
		res.Type = "dir"
	case st.Mode().IsRegular():
		res.Type = "file"
	default:
		res.Type = "other"
	}
	return res, nil
}

// BrowseRoots 由运行时服务收集根目录。
func (s *Service) BrowseRoots(contextRoots []string) BrowseRootsResult {
	dataDir, runtimeDir, globalRoots, extraRoots := s.browseBase()
	roots := BuildBrowseRoots(dataDir, runtimeDir, globalRoots, extraRoots, contextRoots)
	return BrowseRootsResult{
		Roots:         roots,
		Platform:      runtime.GOOS,
		PathSeparator: string(filepath.Separator),
	}
}

// BrowseList 列举目录（服务封装）。
func (s *Service) BrowseList(path, mode string, limit int, _ []string) (BrowseListResult, error) {
	dataDir, runtimeDir, globalRoots, extraRoots := s.browseBase()
	roots := BrowseRootPaths(BuildBrowseRoots(dataDir, runtimeDir, globalRoots, extraRoots, nil))
	return ListBrowseDir(path, roots, mode, limit)
}

// BrowseStat 探测路径（服务封装）。
func (s *Service) BrowseStat(path string, _ []string) (BrowseStatResult, error) {
	dataDir, runtimeDir, globalRoots, extraRoots := s.browseBase()
	roots := BrowseRootPaths(BuildBrowseRoots(dataDir, runtimeDir, globalRoots, extraRoots, nil))
	return StatBrowsePath(path, roots)
}

func (s *Service) browseBase() (dataDir, runtimeDir string, globalRoots, extraRoots []string) {
	if s != nil && s.dataDirFn != nil {
		dataDir = strings.TrimSpace(s.dataDirFn())
	}
	if s != nil {
		runtimeDir = strings.TrimSpace(s.RuntimeDir())
		pol := s.Policy()
		globalRoots = append([]string{}, pol.GlobalFileRoots...)
		extraRoots = append([]string{}, pol.BrowseExtraRoots...)
	}
	return dataDir, runtimeDir, globalRoots, extraRoots
}

func classifyBrowseEntry(full string, info fs.FileInfo, roots []string) (entryType string, enterable, readable bool) {
	mode := info.Mode()
	readable = true
	switch {
	case mode&os.ModeSymlink != 0:
		// 默认按链接展示；若目标是目录且仍在根内则可进入。
		target, err := filepath.EvalSymlinks(full)
		if err != nil {
			return "file", false, false
		}
		targetClean, err := NormalizeBrowsePath(target)
		if err != nil || !PathUnderAnyRoot(targetClean, roots) {
			return "file", false, readable
		}
		tst, err := os.Stat(targetClean)
		if err != nil {
			return "file", false, false
		}
		if tst.IsDir() {
			return "dir", true, true
		}
		return "file", false, true
	case info.IsDir():
		// 目录是否仍在允许根内（通常 join 后仍在）
		enterable = PathUnderAnyRoot(full, roots)
		return "dir", enterable, true
	default:
		return "file", false, true
	}
}

func browseParent(path string, roots []string) string {
	parent := filepath.Dir(path)
	if parent == path {
		return ""
	}
	// 不能跳出所有允许根。
	if !PathUnderAnyRoot(parent, roots) {
		// 若 path 本身就是某个根，则无父级可回。
		return ""
	}
	return parent
}

func pathUnderRoot(path, root string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	rootClean, err := NormalizeBrowsePath(root)
	if err != nil {
		return false
	}
	pathClean := path
	// 统一用 slash 做前缀判定，兼容 Windows 上的 /data 风格根与本地 TempDir。
	pathSlash := strings.TrimRight(filepath.ToSlash(pathClean), "/")
	rootSlash := strings.TrimRight(filepath.ToSlash(rootClean), "/")
	if runtime.GOOS == "windows" {
		pathSlash = strings.ToLower(pathSlash)
		rootSlash = strings.ToLower(rootSlash)
	}
	if pathSlash == rootSlash {
		return true
	}
	if rootSlash == "" {
		return false
	}
	if strings.HasPrefix(pathSlash, rootSlash+"/") {
		return true
	}
	// 回退 native Rel，处理盘符等边界
	rel, err := filepath.Rel(rootClean, pathClean)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func isAbsPath(p string) bool {
	if filepath.IsAbs(p) {
		return true
	}
	// Unix 风格绝对路径在 Windows 测试中也视为绝对，便于容器路径。
	if strings.HasPrefix(p, "/") {
		return true
	}
	// Windows 盘符路径
	if len(p) >= 2 && p[1] == ':' {
		return true
	}
	return false
}
