package runtime

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Dockerfile.full 内置的 Node 与受管预置安装的默认 Node 必须同版本、同校验和：
// runtime/node/bin 在 PATH 上先于 /usr/local/bin，两者版本不一致会让「是否装过受管包」
// 决定实际生效的 node，产生难以排查的行为差异。本测试锁死两处的一致性。
func TestDockerfileFullNodePinMatchesCatalog(t *testing.T) {
	t.Parallel()

	spec, ok := FindPackage(DefaultNodePackageID)
	if !ok {
		t.Fatalf("默认 Node 预置包 %q 不在目录中", DefaultNodePackageID)
	}

	dockerfile := readRepoFile(t, "Dockerfile.full")
	if got := dockerfileArg(t, dockerfile, "NODE_VERSION"); got != spec.Version {
		t.Fatalf("Dockerfile.full NODE_VERSION=%q，catalog 为 %q；升级预置 Node 时需同步镜像", got, spec.Version)
	}

	for _, arch := range []struct{ goarch, arg string }{
		{goarch: "amd64", arg: "NODE_SHA256_AMD64"},
		{goarch: "arm64", arg: "NODE_SHA256_ARM64"},
	} {
		asset, found := assetForGOARCH(spec, arch.goarch)
		if !found {
			t.Fatalf("catalog 缺少 linux/%s 的 Node 资产", arch.goarch)
		}
		if got := dockerfileArg(t, dockerfile, arch.arg); got != asset.SHA256 {
			t.Fatalf("Dockerfile.full %s=%q，catalog 为 %q", arch.arg, got, asset.SHA256)
		}
	}
}

// 发行版 nodejs/npm 与 python3-pip 已被移除：前者在 Debian bookworm 是 EOL 的 Node 18 且与
// 受管版本冲突，后者不被任何产品路径使用（pip 依赖走受管 uv，且 pip 不在命令白名单内）。
func TestDockerfileFullDoesNotUseDistroNodeOrPip(t *testing.T) {
	t.Parallel()

	dockerfile := readRepoFile(t, "Dockerfile.full")
	for _, pkg := range []string{"nodejs", "npm", "python3-pip"} {
		pattern := regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(pkg) + `\s*\\?\s*$`)
		if pattern.MatchString(dockerfile) {
			t.Fatalf("Dockerfile.full 不应通过 apt 安装 %q", pkg)
		}
	}
}

func assetForGOARCH(spec PackageSpec, goarch string) (PackageAsset, bool) {
	for _, asset := range spec.Assets {
		if asset.GOOS == "linux" && asset.GOARCH == goarch {
			return asset, true
		}
	}
	return PackageAsset{}, false
}

// dockerfileArg 读取形如 `ARG NAME=value` 的固定值构建参数。
func dockerfileArg(t *testing.T, dockerfile, name string) string {
	t.Helper()
	pattern := regexp.MustCompile(`(?m)^ARG\s+` + regexp.QuoteMeta(name) + `=(\S+)\s*$`)
	match := pattern.FindStringSubmatch(dockerfile)
	if match == nil {
		t.Fatalf("Dockerfile.full 缺少 ARG %s=...", name)
	}
	return match[1]
}

// readRepoFile 读取仓库根目录下的文件（测试工作目录为包目录）。
func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", name, err)
	}
	return string(b)
}
