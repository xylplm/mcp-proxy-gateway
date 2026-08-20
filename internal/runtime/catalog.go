package runtime

import (
	"runtime"
	"strings"
)

// PackageKind 标识预置包类型（决定解压与落盘布局）。
type PackageKind string

const (
	PackageKindNode PackageKind = "node"
	PackageKindUV   PackageKind = "uv"

	// DefaultNodePackageID 是新安装请求与运行时预检默认使用的 Node 版本。
	// 旧版本仍保留在目录中，便于已有环境回滚或兼容。
	DefaultNodePackageID = "node-24.19.0"
)

// MirrorAsset 为官方资产的一个镜像源（url 与官方一致，内容字节相同，复用同一 SHA256）。
// 用于在国内等官方源不可达时自动回退；镜像必须与官方 tarball 字节一致。
type MirrorAsset struct {
	URL string `json:"url"`
}

// PackageAsset 为某一 GOOS/GOARCH 的下载资产（URL + 完整性校验）。
type PackageAsset struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	// Format: "tar.gz" | "zip"
	Format string `json:"format"`
	// Mirrors 为按序回退的镜像源（与官方 URL 同一文件，复用同一 SHA256）。
	Mirrors []MirrorAsset `json:"mirrors,omitempty"`
}

// downloadSources 返回官方 URL 及其镜像，按安装顺序排列（官方优先，镜像兜底）。
func (a PackageAsset) downloadSources() []downloadSource {
	out := []downloadSource{{url: a.URL}}
	for _, m := range a.Mirrors {
		out = append(out, downloadSource{url: m.URL, mirror: true})
	}
	return out
}

// PackageSpec 为受控预置包目录项（固定清单，禁止任意 URL）。
type PackageSpec struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	Description string         `json:"description"`
	Kind        PackageKind    `json:"kind"`
	Tools       []string       `json:"tools"`
	Assets      []PackageAsset `json:"assets"`
}

// CatalogPackage 为管理台展示用目录项（含当前平台是否可装、是否已装）。
// 不嵌入 PackageSpec，避免把所有平台的下载 URL 与 SHA256 暴露到管理台响应。
type CatalogPackage struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Version     string      `json:"version"`
	Description string      `json:"description"`
	Kind        PackageKind `json:"kind"`
	Tools       []string    `json:"tools"`
	Supported   bool        `json:"supported"`
	Installed   bool        `json:"installed"`
	InstalledAt string      `json:"installedAt,omitempty"`
	AssetGOOS   string      `json:"assetGoos,omitempty"`
	AssetGOARCH string      `json:"assetGoarch,omitempty"`
}

// nodeMirrors 返回 Node 官方资产在国内的镜像源（npmmirror 字节与官方一致，SHASUMS256 复用）。
func nodeMirrors(version, file string) []MirrorAsset {
	return []MirrorAsset{
		{URL: "https://registry.npmmirror.com/-/binary/node/v" + version + "/" + file},
	}
}

// uvMirrors 返回 uv 官方资产在国内的代理镜像（转发到 GitHub release，字节一致）。
func uvMirrors(file string) []MirrorAsset {
	official := "https://github.com/astral-sh/uv/releases/download/0.6.14/" + file
	return []MirrorAsset{
		{URL: "https://gh-proxy.com/" + official},
		{URL: "https://ghproxy.net/" + official},
	}
}

// DefaultCatalog 返回内置受控预置清单（版本与校验和写死，升级需发版）。
//
// 校验和来自官方发行渠道（Node SHASUMS256 / uv 发布 sha256）。
// 镜像源字节与官方一致，复用同一 SHA256，仅在官方源不可达时按序回退。
func DefaultCatalog() []PackageSpec {
	return []PackageSpec{
		{
			ID:          DefaultNodePackageID,
			Name:        "Node.js",
			Version:     "24.19.0",
			Description: "官方 Node.js 24 LTS 发行版，提供 node / npx / npm，安装到 runtime/node。",
			Kind:        PackageKindNode,
			Tools:       []string{"node", "npx", "npm"},
			Assets: []PackageAsset{
				{
					GOOS:    "linux",
					GOARCH:  "amd64",
					URL:     "https://nodejs.org/dist/v24.19.0/node-v24.19.0-linux-x64.tar.gz",
					SHA256:  "f625d97cd707df4ff96254916fbc5ff014f09c09effe5a1e0ca8f6d41a8789d4",
					Format:  "tar.gz",
					Mirrors: nodeMirrors("24.19.0", "node-v24.19.0-linux-x64.tar.gz"),
				},
				{
					GOOS:    "linux",
					GOARCH:  "arm64",
					URL:     "https://nodejs.org/dist/v24.19.0/node-v24.19.0-linux-arm64.tar.gz",
					SHA256:  "d28c8a5bf0a808f0ed434a1dce8c54ae98f0371c0bd86ac58abc613f73e6643f",
					Format:  "tar.gz",
					Mirrors: nodeMirrors("24.19.0", "node-v24.19.0-linux-arm64.tar.gz"),
				},
			},
		},
		{
			ID:          "node-22.14.0",
			Name:        "Node.js",
			Version:     "22.14.0",
			Description: "官方 Node.js 22 LTS 发行版，提供 node / npx / npm，安装到 runtime/node。",
			Kind:        PackageKindNode,
			Tools:       []string{"node", "npx", "npm"},
			Assets: []PackageAsset{
				{
					GOOS:    "linux",
					GOARCH:  "amd64",
					URL:     "https://nodejs.org/dist/v22.14.0/node-v22.14.0-linux-x64.tar.gz",
					SHA256:  "9d942932535988091034dc94cc5f42b6dc8784d6366df3a36c4c9ccb3996f0c2",
					Format:  "tar.gz",
					Mirrors: nodeMirrors("22.14.0", "node-v22.14.0-linux-x64.tar.gz"),
				},
				{
					GOOS:    "linux",
					GOARCH:  "arm64",
					URL:     "https://nodejs.org/dist/v22.14.0/node-v22.14.0-linux-arm64.tar.gz",
					SHA256:  "8cf30ff7250f9463b53c18f89c6c606dfda70378215b2c905d0a9a8b08bd45e0",
					Format:  "tar.gz",
					Mirrors: nodeMirrors("22.14.0", "node-v22.14.0-linux-arm64.tar.gz"),
				},
			},
		},
		{
			ID:          "uv-0.6.14",
			Name:        "uv",
			Version:     "0.6.14",
			Description: "Astral uv 官方二进制，提供 uv / uvx，安装到 runtime/uv。",
			Kind:        PackageKindUV,
			Tools:       []string{"uv", "uvx"},
			Assets: []PackageAsset{
				{
					GOOS:    "linux",
					GOARCH:  "amd64",
					URL:     "https://github.com/astral-sh/uv/releases/download/0.6.14/uv-x86_64-unknown-linux-musl.tar.gz",
					SHA256:  "0cac4df0cb3457b154f2039ae471e89cd4e15f3bd790bbb3cb0b8b40d940b93e",
					Format:  "tar.gz",
					Mirrors: uvMirrors("uv-x86_64-unknown-linux-musl.tar.gz"),
				},
				{
					GOOS:    "linux",
					GOARCH:  "arm64",
					URL:     "https://github.com/astral-sh/uv/releases/download/0.6.14/uv-aarch64-unknown-linux-musl.tar.gz",
					SHA256:  "94e22c4be44d205def456427639ca5ca1c1a9e29acc31808a7b28fdd5dcf7f17",
					Format:  "tar.gz",
					Mirrors: uvMirrors("uv-aarch64-unknown-linux-musl.tar.gz"),
				},
			},
		},
	}
}

// FindPackage 按 id 查找目录项。
func FindPackage(id string) (PackageSpec, bool) {
	id = strings.TrimSpace(id)
	for _, p := range DefaultCatalog() {
		if p.ID == id {
			return p, true
		}
	}
	return PackageSpec{}, false
}

// SelectAsset 为当前（或指定）平台选择资产。
func SelectAsset(spec PackageSpec, goos, goarch string) (PackageAsset, bool) {
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	// 兼容 arm → arm64 不在此映射；仅精确匹配。
	for _, a := range spec.Assets {
		if a.GOOS == goos && a.GOARCH == goarch {
			return a, true
		}
	}
	return PackageAsset{}, false
}
