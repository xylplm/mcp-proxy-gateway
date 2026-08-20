package runtime

import (
	"errors"
	"strings"
	"testing"
)

func TestDetectManagedRuntimeSupport(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		goos        string
		marker      func(string) bool
		supported   bool
		reasonMatch string
	}{
		{
			name:      "official Linux image is supported",
			goos:      "linux",
			marker:    func(path string) bool { return path == managedRuntimeMarkerPath },
			supported: true,
		},
		{
			name:        "Linux without marker is unsupported",
			goos:        "linux",
			marker:      func(string) bool { return false },
			reasonMatch: "官方 Linux Docker/OCI 镜像",
		},
		{
			name:        "Windows is unsupported even with marker",
			goos:        "windows",
			marker:      func(string) bool { return true },
			reasonMatch: "Windows",
		},
		{
			name:        "macOS is unsupported even with marker",
			goos:        "darwin",
			marker:      func(string) bool { return true },
			reasonMatch: "macOS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			support := detectManagedRuntimeSupport(tt.goos, tt.marker)
			if support.Supported != tt.supported {
				t.Fatalf("Supported=%v, want %v; result=%+v", support.Supported, tt.supported, support)
			}
			if tt.reasonMatch != "" && !strings.Contains(support.Reason, tt.reasonMatch) {
				t.Fatalf("Reason=%q, expected it to contain %q", support.Reason, tt.reasonMatch)
			}
		})
	}
}

func TestRequireManagedRuntimeSupportReturnsTypedError(t *testing.T) {
	t.Parallel()
	err := requireManagedRuntimeSupport(RuntimeManagementSupport{Reason: "unsupported for test"})
	if !errors.Is(err, ErrManagedRuntimeUnsupported) {
		t.Fatalf("error=%v does not wrap ErrManagedRuntimeUnsupported", err)
	}
	if err := requireManagedRuntimeSupport(RuntimeManagementSupport{Supported: true}); err != nil {
		t.Fatalf("supported environment returned error: %v", err)
	}
}
