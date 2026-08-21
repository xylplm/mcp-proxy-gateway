package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDoctorProbeAndSummary(t *testing.T) {
	t.Parallel()
	d := NewDoctor(func(file string) (string, error) {
		switch file {
		case "node", "npx":
			return "/usr/bin/" + file, nil
		default:
			return "", fmt.Errorf("not found")
		}
	})
	tools := d.Probe()
	if len(tools) != len(DefaultProbeTools()) {
		t.Fatalf("tool count=%d", len(tools))
	}
	sum := BuildSummary(DefaultPolicy(), tools, "/data", "/data/runtime", []string{"/data/runtime/bin"}, FlavorFull)
	if sum.AvailableCount != 2 {
		t.Fatalf("available=%d", sum.AvailableCount)
	}
	if sum.MissingCount != len(tools)-2 {
		t.Fatalf("missing=%d", sum.MissingCount)
	}
	if sum.DataDir != "/data" {
		t.Fatalf("dataDir=%q", sum.DataDir)
	}
	if sum.RuntimeDir != "/data/runtime" {
		t.Fatalf("runtimeDir=%q", sum.RuntimeDir)
	}
	if len(sum.PathPrefixes) != 1 {
		t.Fatalf("pathPrefixes=%v", sum.PathPrefixes)
	}
	if !sum.StdioEnabled {
		t.Fatal("stdio should be enabled")
	}
	if sum.ImageFlavor != string(FlavorFull) || !sum.LocalRuntimeSupported {
		t.Fatalf("flavor=%q localRuntimeSupported=%v", sum.ImageFlavor, sum.LocalRuntimeSupported)
	}
	if len(sum.CommandAllowlist) == 0 {
		t.Fatal("allowlist should be present for default policy")
	}
	if sum.Sandbox.Platform == "" {
		t.Fatal("sandbox platform should be set")
	}
	foundGuide := false
	for _, n := range sum.RiskNotes {
		if strings.Contains(n, "runtime") || strings.Contains(n, "镜像") {
			foundGuide = true
			break
		}
	}
	if !foundGuide {
		t.Fatalf("expected runtime guide in notes: %v", sum.RiskNotes)
	}
}

func TestBuildSummarySlimFlavorLeadsWithImageNotice(t *testing.T) {
	t.Parallel()
	sum := BuildSummary(DefaultPolicy(), nil, "/data", "/data/runtime", nil, FlavorSlim)
	if sum.ImageFlavor != string(FlavorSlim) || sum.LocalRuntimeSupported {
		t.Fatalf("flavor=%q localRuntimeSupported=%v", sum.ImageFlavor, sum.LocalRuntimeSupported)
	}
	if len(sum.RiskNotes) == 0 || !strings.Contains(sum.RiskNotes[0], "精简镜像") {
		t.Fatalf("slim notice must lead risk notes: %v", sum.RiskNotes)
	}
	// 精简镜像不提供本地运行时，不应给出「放入 runtime/bin 覆盖版本」的引导。
	for _, n := range sum.RiskNotes {
		if strings.Contains(n, "覆盖版本") {
			t.Fatalf("slim image must not advertise runtime override: %v", sum.RiskNotes)
		}
	}
}

func TestCurrentImageFlavorReadsEnv(t *testing.T) {
	t.Setenv("MPG_IMAGE_FLAVOR", "slim")
	if got := CurrentImageFlavor(); got != FlavorSlim {
		t.Fatalf("flavor=%q", got)
	}
	t.Setenv("MPG_IMAGE_FLAVOR", " SLIM ")
	if got := CurrentImageFlavor(); got != FlavorSlim {
		t.Fatalf("flavor should be case/space insensitive: %q", got)
	}
	// 未声明（源码构建/开发环境）按完整能力处理，不能因缺变量藏功能。
	t.Setenv("MPG_IMAGE_FLAVOR", "")
	if got := CurrentImageFlavor(); got != FlavorFull {
		t.Fatalf("flavor=%q", got)
	}
	t.Setenv("MPG_IMAGE_FLAVOR", "unexpected")
	if got := CurrentImageFlavor(); got != FlavorFull {
		t.Fatalf("unknown flavor must fall back to full: %q", got)
	}
}

func TestDoctorProbeReportsMissingExecutablePermission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 没有统一的 Unix 可执行位")
	}
	path := filepath.Join(t.TempDir(), "node")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho node\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := NewDoctor(func(string) (string, error) { return path, nil })
	tools := d.Probe()
	if len(tools) == 0 || tools[0].Available || tools[0].Warning == "" || tools[0].Path != path {
		t.Fatalf("expected permission warning, got %+v", tools)
	}
}

func newSlimService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	rt := filepath.Join(data, "runtime")
	svc := NewService(
		func() Policy { return DefaultPolicy() },
		func() string { return data },
		func() string { return rt },
	)
	svc.flavorFn = func() ImageFlavor { return FlavorSlim }
	return svc, rt
}

func TestServiceSummaryOnSlimImageOmitsDependencyState(t *testing.T) {
	svc, rt := newSlimService(t)
	sum := svc.Summary()
	if sum.RuntimeDir != rt {
		t.Fatalf("runtimeDir=%q", sum.RuntimeDir)
	}
	if sum.LocalRuntimeSupported || sum.ImageFlavor != string(FlavorSlim) {
		t.Fatalf("slim image must not advertise local runtime: %+v", sum)
	}
	if sum.Deps != nil {
		t.Fatalf("slim image has no dependency area: %+v", sum.Deps)
	}
	// 运行时卷仍然建立：runtime/bin 是用户手放可执行文件的逃生口，与镜像形态无关。
	if !sum.LayoutReady {
		t.Fatal("runtime layout should still be prepared")
	}
	if st, err := os.Stat(filepath.Join(rt, RuntimeSubdirBin)); err != nil || !st.IsDir() {
		t.Fatalf("runtime bin should exist: %v", err)
	}
}

func TestServiceSummaryInitializesLayoutOnce(t *testing.T) {
	svc, rt := newSlimService(t)
	_ = svc.Summary()
	if svc.layoutDir != rt {
		t.Fatalf("layout marker=%q, want %q", svc.layoutDir, rt)
	}
	// 目录被外部删掉后不再重复创建：标记只为避免每次 summary 都打一遍 mkdir。
	if err := os.RemoveAll(rt); err != nil {
		t.Fatal(err)
	}
	_ = svc.Summary()
	if _, err := os.Stat(rt); !os.IsNotExist(err) {
		t.Fatalf("layout should not be recreated within the same service: %v", err)
	}
}

func TestServiceRejectsDependencyActionsOnSlimImage(t *testing.T) {
	svc, _ := newSlimService(t)

	actions := []struct {
		name string
		call func() error
	}{
		{
			name: "list dependencies",
			call: func() error {
				_, err := svc.ListDeps(context.Background(), DepKindNpm)
				return err
			},
		},
		{
			name: "install dependency",
			call: func() error {
				_, err := svc.InstallDep(context.Background(), DepKindNpm, "lodash")
				return err
			},
		},
		{
			name: "uninstall dependency",
			call: func() error {
				_, err := svc.UninstallDep(context.Background(), DepKindNpm, "lodash")
				return err
			},
		},
	}

	for _, action := range actions {
		t.Run(action.name, func(t *testing.T) {
			err := action.call()
			if !errors.Is(err, ErrLocalRuntimeUnsupported) {
				t.Fatalf("error=%v, expected ErrLocalRuntimeUnsupported", err)
			}
			if !strings.Contains(err.Error(), ":slim") && !strings.Contains(err.Error(), "完整镜像") {
				t.Fatalf("error should guide users to the full image: %v", err)
			}
		})
	}
}
