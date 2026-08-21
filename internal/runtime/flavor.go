package runtime

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ImageFlavor 标识镜像形态。
//
// 由镜像在构建时通过 MPG_IMAGE_FLAVOR 声明，不是用户可在管理台切换的运行期开关：
// 精简镜像不含任何本地运行时，暴露 stdio 相关功能只会让用户点到必然失败的入口。
type ImageFlavor string

const (
	// FlavorFull 完整镜像：内置 Node / Python / uv，本地 stdio 功能全部可用。
	FlavorFull ImageFlavor = "full"
	// FlavorSlim 精简镜像：只有网关本体，仅支持远程上游。
	FlavorSlim ImageFlavor = "slim"
)

// ErrLocalRuntimeUnsupported 表示当前镜像不提供本地运行时。
// 管理 API 用 errors.Is 归一化为校验类错误，而不是内部错误。
var ErrLocalRuntimeUnsupported = errors.New("当前镜像不含本地运行时")

// CurrentImageFlavor 读取镜像形态。
//
// 未声明时按 full 处理：源码构建与开发环境默认具备完整能力，
// 不应因为缺一个环境变量就把功能藏起来。
func CurrentImageFlavor() ImageFlavor {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("MPG_IMAGE_FLAVOR")), string(FlavorSlim)) {
		return FlavorSlim
	}
	return FlavorFull
}

// LocalRuntimeSupported 表示本镜像是否提供本地 stdio 运行时。
func (f ImageFlavor) LocalRuntimeSupported() bool {
	return f != FlavorSlim
}

// requireLocalRuntime 供依赖管理等本地运行时相关操作前置校验。
func requireLocalRuntime(flavor ImageFlavor) error {
	if flavor.LocalRuntimeSupported() {
		return nil
	}
	return fmt.Errorf("%w：精简镜像仅支持远程上游，请改用完整镜像（:latest / :full）", ErrLocalRuntimeUnsupported)
}
