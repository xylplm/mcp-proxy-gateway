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
	RuntimeSubdirNpm    = "npm"
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
		RuntimeSubdirNpm,
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
	return "MCP Proxy Gateway — Linux 容器受管运行时目录\n" +
		"\n" +
		"本目录仅由官方 Linux Docker/OCI 镜像管理。原生 Windows/macOS 和 Windows 容器不支持\n" +
		"运行环境页的安装、卸载和依赖管理；Windows/macOS Docker Desktop 请使用 Linux 容器引擎。\n" +
		"\n" +
		"将 Node / Python / uv 等可执行文件放入本目录后，stdio 上游即可被探测与启动。\n" +
		"本目录位于数据卷内，容器更新不会丢失。\n" +
		"\n" +
		"推荐布局：\n" +
		"  bin/           用户手动放置的直接可执行文件（优先加入 PATH）\n" +
		"  node/bin/      受管 Node 发行版\n" +
		"  npm/           受管 npm 共享依赖、node_modules 与 CLI shim\n" +
		"  python/bin/    用户手动放置的 Python 发行版\n" +
		"  uv/bin/        受管 uv 发行版\n" +
		"  cache/         npm / uv 受管缓存\n" +
		"  state/         受管安装状态\n" +
		"\n" +
		"npm 共享依赖适用于受管 CLI 与 CommonJS 兼容查询；ESM 项目应维护自己的本地依赖。\n" +
		"pip 依赖管理需要镜像中可用 Python（官方 :full 镜像已提供）。\n" +
		"\n" +
		"当前路径：" + runtimeDir + "\n" +
		"放置文件后请刷新运行环境探测。也可设置 MPG_RUNTIME_DIR 覆盖本目录位置。\n"
}

// invalidatePathPrefixes 清除指定运行时目录的 PATH 前缀缓存。
// 安装/卸载完成后立即调用，避免短 TTL 内继续使用旧探测结果。
func invalidatePathPrefixes(runtimeDir string) {
	runtimeDir = strings.TrimSpace(runtimeDir)
	pathPrefixesCache.Lock()
	if runtimeDir == "" || pathPrefixesCache.dir == runtimeDir {
		pathPrefixesCache.dir = ""
		pathPrefixesCache.at = time.Time{}
		pathPrefixesCache.prefixes = nil
	}
	pathPrefixesCache.Unlock()
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
		filepath.Join(runtimeDir, RuntimeSubdirNpm, "node_modules", ".bin"),
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
