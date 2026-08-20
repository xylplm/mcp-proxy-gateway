package runtime

import (
	"errors"
	"fmt"
	"os"
	goruntime "runtime"
)

// managedRuntimeMarkerPath is created only by the official Linux Docker/OCI images.
// It is a product-support marker, not a container-security boundary.
const managedRuntimeMarkerPath = "/usr/share/mcp-proxy-gateway/managed-runtime"

// ErrManagedRuntimeUnsupported indicates that managed runtime installation and
// dependency operations are unavailable in the current process environment.
var ErrManagedRuntimeUnsupported = errors.New("受管运行时仅支持官方 Linux Docker/OCI 镜像")

// RuntimeManagementSupport describes whether this process may mutate the managed
// runtime volume. Remote upstreams and ordinary stdio policy validation are not
// restricted by this capability.
type RuntimeManagementSupport struct {
	Supported bool   `json:"managementSupported"`
	Reason    string `json:"managementReason,omitempty"`
}

// ManagedRuntimeSupport reports the product support boundary for managed Node,
// uv, npm, and pip operations. Native Windows/macOS processes and Windows
// containers are intentionally unsupported; Docker Desktop is supported only
// through its Linux-container engine and the official image.
func ManagedRuntimeSupport() RuntimeManagementSupport {
	return detectManagedRuntimeSupport(goruntime.GOOS, func(path string) bool {
		st, err := os.Stat(path)
		return err == nil && !st.IsDir()
	})
}

func detectManagedRuntimeSupport(goos string, markerExists func(string) bool) RuntimeManagementSupport {
	if goos != "linux" {
		return RuntimeManagementSupport{Reason: "受管运行时安装与依赖管理仅支持官方 Linux Docker/OCI 镜像；原生 Windows/macOS 和 Windows 容器不受支持"}
	}
	if markerExists == nil || !markerExists(managedRuntimeMarkerPath) {
		return RuntimeManagementSupport{Reason: "当前不是官方 Linux Docker/OCI 镜像，无法使用受管运行时安装与依赖管理"}
	}
	return RuntimeManagementSupport{Supported: true}
}

// requireManagedRuntimeSupport 返回稳定的可判定错误，供管理 API 归一化为校验类响应。
func requireManagedRuntimeSupport(support RuntimeManagementSupport) error {
	if support.Supported {
		return nil
	}
	if support.Reason == "" {
		return ErrManagedRuntimeUnsupported
	}
	return fmt.Errorf("%w：%s", ErrManagedRuntimeUnsupported, support.Reason)
}
