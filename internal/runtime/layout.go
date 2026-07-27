package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const pathPrefixesCacheTTL = 5 * time.Second

var pathPrefixesCache struct {
	sync.RWMutex
	dir      string
	at       time.Time
	prefixes []string
}

// 卷内运行时目录子路径（稳定契约，供 entrypoint / 文档 / P1b 共用）。
const (
	RuntimeSubdirBin    = "bin"
	RuntimeSubdirNode   = "node"
	RuntimeSubdirPython = "python"
	RuntimeSubdirUV     = "uv"
	RuntimeSubdirCache  = "cache"
	RuntimeSubdirState  = "state"
	RuntimeReadmeName   = "README.txt"
)

// ResolveRuntimeDir 解析运行时根目录。
//
// override 非空时：Clean 后使用（相对路径相对于 dataDir）。
// override 为空时：filepath.Join(dataDir, "runtime")；dataDir 亦空则返回 ""。
func ResolveRuntimeDir(dataDir, override string) string {
	dataDir = strings.TrimSpace(dataDir)
	override = strings.TrimSpace(override)
	if override != "" {
		// Unix 风格绝对路径（如 /data/runtime）在 Windows 上 filepath.IsAbs 为 false，
		// 仍按绝对路径处理，便于容器路径约定与跨平台测试一致。
		isAbs := filepath.IsAbs(override) || strings.HasPrefix(override, "/")
		cleaned := filepath.Clean(override)
		if !isAbs && dataDir != "" {
			cleaned = filepath.Clean(filepath.Join(dataDir, cleaned))
		}
		if cleaned == "." || cleaned == string(filepath.Separator) {
			return ""
		}
		return cleaned
	}
	if dataDir == "" {
		return ""
	}
	return filepath.Clean(filepath.Join(dataDir, "runtime"))
}

// EnsureRuntimeLayout 幂等创建卷内运行时目录结构（不下载任何内容）。
func EnsureRuntimeLayout(runtimeDir string) error {
	runtimeDir = strings.TrimSpace(runtimeDir)
	if runtimeDir == "" {
		return nil
	}
	subs := []string{
		RuntimeSubdirBin,
		RuntimeSubdirNode,
		RuntimeSubdirPython,
		RuntimeSubdirUV,
		RuntimeSubdirCache,
		RuntimeSubdirState,
	}
	for _, sub := range subs {
		if err := os.MkdirAll(filepath.Join(runtimeDir, sub), 0o755); err != nil {
			return err
		}
	}
	readme := filepath.Join(runtimeDir, RuntimeReadmeName)
	if _, err := os.Stat(readme); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	content := runtimeReadmeContent(runtimeDir)
	return os.WriteFile(readme, []byte(content), 0o644)
}

func runtimeReadmeContent(runtimeDir string) string {
	return "" +
		"MCP Proxy Gateway — 本地运行时目录\n" +
		"\n" +
		"将 Node / Python / uv 等可执行文件放入本目录后，stdio 上游即可被探测与启动。\n" +
		"默认镜像不含这些工具；本目录位于数据卷内，容器更新不会丢失。\n" +
		"\n" +
		"推荐布局：\n" +
		"  bin/           直接放置 node、npx、uv、uvx 等（优先加入 PATH）\n" +
		"  node/bin/      Node 发行版\n" +
		"  python/bin/    Python 发行版\n" +
		"  uv/bin/        uv 发行版\n" +
		"  cache/         包管理器缓存（预留）\n" +
		"  state/         安装状态（预留）\n" +
		"\n" +
		"当前路径：" + runtimeDir + "\n" +
		"放置文件后请重启网关进程，并在管理台「运行环境」刷新探测。\n" +
		"也可设置环境变量 MPG_RUNTIME_DIR 覆盖本目录位置。\n"
}

// PathPrefixes 返回应优先加入 PATH 的已存在子目录（稳定顺序）。
func PathPrefixes(runtimeDir string) []string {
	runtimeDir = strings.TrimSpace(runtimeDir)
	if runtimeDir == "" {
		return nil
	}
	now := time.Now()
	pathPrefixesCache.RLock()
	if pathPrefixesCache.dir == runtimeDir && now.Sub(pathPrefixesCache.at) < pathPrefixesCacheTTL {
		prefixes := append([]string{}, pathPrefixesCache.prefixes...)
		pathPrefixesCache.RUnlock()
		return prefixes
	}
	pathPrefixesCache.RUnlock()

	candidates := []string{
		filepath.Join(runtimeDir, RuntimeSubdirBin),
		filepath.Join(runtimeDir, RuntimeSubdirNode, "bin"),
		filepath.Join(runtimeDir, RuntimeSubdirPython, "bin"),
		filepath.Join(runtimeDir, RuntimeSubdirUV, "bin"),
	}
	out := make([]string, 0, len(candidates))
	for _, dir := range candidates {
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			continue
		}
		out = append(out, dir)
	}
	pathPrefixesCache.Lock()
	pathPrefixesCache.dir = runtimeDir
	pathPrefixesCache.at = now
	pathPrefixesCache.prefixes = append([]string{}, out...)
	pathPrefixesCache.Unlock()
	return out
}

// PrependPath 将 prefixes 幂等前置到 PATH 字符串（使用 OS 路径分隔符）。
func PrependPath(current string, prefixes []string) string {
	if len(prefixes) == 0 {
		return current
	}
	sep := string(os.PathListSeparator)
	existing := splitPathList(current)
	seen := make(map[string]struct{}, len(existing)+len(prefixes))
	for _, p := range existing {
		seen[pathKey(p)] = struct{}{}
	}
	head := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		k := pathKey(p)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		head = append(head, p)
	}
	if len(head) == 0 {
		return current
	}
	if current == "" {
		return strings.Join(head, sep)
	}
	return strings.Join(head, sep) + sep + current
}

func splitPathList(path string) []string {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, string(os.PathListSeparator))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func pathKey(p string) string {
	return strings.ToLower(filepath.Clean(p))
}
