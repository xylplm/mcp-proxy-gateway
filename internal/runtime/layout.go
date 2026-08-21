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

// 卷内运行时目录子路径（稳定契约，供 entrypoint / 文档共用）。
//
// Node / Python / uv 解释器本身由镜像提供，不落在数据卷里；卷只保存
// 用户自放的可执行文件与 npm / pip 共享依赖。
const (
	// RuntimeSubdirBin 用户手动放置的可执行文件，PATH 优先级最高。
	RuntimeSubdirBin = "bin"
	// RuntimeSubdirNpm npm 共享依赖前缀（node_modules 与 node_modules/.bin）。
	RuntimeSubdirNpm = "npm"
	// RuntimeSubdirPip pip 共享依赖目录（uv pip --target，顶层即 site-packages，脚本在 bin/）。
	RuntimeSubdirPip = "pip"
	// RuntimeSubdirCache npm / uv 缓存，避免写入容器临时层。
	RuntimeSubdirCache = "cache"
	RuntimeReadmeName  = "README.txt"
)

// runtimeLayoutSubdirs 是 EnsureRuntimeLayout 创建的目录集合（稳定顺序）。
func runtimeLayoutSubdirs() []string {
	return []string{
		RuntimeSubdirBin,
		RuntimeSubdirNpm,
		RuntimeSubdirPip,
		RuntimeSubdirCache,
	}
}

// runtimePathCandidates 返回运行时卷内可能提供可执行文件的目录（稳定顺序，含不存在项）。
//
// 与 runtimeIntermediateDirs 成对维护：每个候选项从自身往上走过若干中间目录后
// 必须回到 runtimeDir，严格档的运行时根判定才成立。
func runtimePathCandidates(runtimeDir string) []string {
	return []string{
		filepath.Join(runtimeDir, RuntimeSubdirBin),
		filepath.Join(runtimeDir, RuntimeSubdirNpm, "node_modules", ".bin"),
		filepath.Join(runtimeDir, RuntimeSubdirPip, "bin"),
	}
}

// runtimePathSuffixes 返回 PATH 前缀相对运行时根的路径后缀。
//
// 与 runtimePathCandidates 同源，供严格档由前缀反推运行时根使用，
// 避免两处各自维护一份布局知识。
func runtimePathSuffixes() []string {
	return runtimePathCandidates("")
}

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
	for _, sub := range runtimeLayoutSubdirs() {
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
	return "MCP Proxy Gateway — 卷内运行时目录\n" +
		"\n" +
		"Node / Python / uv / uvx 由完整镜像（:latest / :full）内置，不在运行期下载。\n" +
		"精简镜像（:slim）不含任何本地运行时，仅支持远程与 OpenAPI 上游。\n" +
		"本目录位于数据卷内，容器更新不会丢失。\n" +
		"\n" +
		"布局：\n" +
		"  bin/                      用户手动放置的可执行文件（PATH 优先级最高）\n" +
		"  npm/node_modules/         npm 共享依赖；node_modules/.bin 自动加入 PATH\n" +
		"  pip/                      pip 共享依赖（顶层即 site-packages，自动加入 PYTHONPATH）\n" +
		"  pip/bin/                  pip 包提供的命令行脚本，自动加入 PATH\n" +
		"  cache/                    npm / uv 下载缓存\n" +
		"\n" +
		"npm 共享依赖适用于 CLI 与 CommonJS 兼容查询；ESM 项目应维护自己的本地依赖。\n" +
		"需要覆盖镜像自带版本时，把可执行文件放入 bin/ 即可优先生效。\n" +
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

	candidates := runtimePathCandidates(runtimeDir)
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
