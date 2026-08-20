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
	sum := BuildSummary(DefaultPolicy(), tools, "/data", "/data/runtime", []string{"/data/runtime/bin"}, nil, nil, nil, nil, "", RuntimeManagementSupport{Supported: true})
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
	if len(sum.CommandAllowlist) == 0 {
		t.Fatal("allowlist should be present for default policy")
	}
	if sum.Sandbox.Platform == "" {
		t.Fatal("sandbox platform should be set")
	}
	foundGuide := false
	for _, n := range sum.RiskNotes {
		if strings.Contains(n, "runtime") || strings.Contains(n, "预置") {
			foundGuide = true
			break
		}
	}
	if !foundGuide {
		t.Fatalf("expected runtime guide in notes: %v", sum.RiskNotes)
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

func TestServiceSummaryDoesNotCreateManagedLayoutOutsideOfficialImage(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	rt := filepath.Join(data, "runtime")
	svc := NewService(
		func() Policy { return DefaultPolicy() },
		func() string { return data },
		func() string { return rt },
	)
	svc.supportFn = func() RuntimeManagementSupport {
		return RuntimeManagementSupport{Reason: "test unsupported"}
	}
	sum := svc.Summary()
	if sum.RuntimeDir != rt {
		t.Fatalf("runtimeDir=%q", sum.RuntimeDir)
	}
	if sum.ManagementSupported {
		t.Fatal("development process must not advertise official-image runtime management")
	}
	if sum.ManagementReason == "" {
		t.Fatal("unsupported management reason is required")
	}
	if sum.LayoutReady {
		t.Fatal("summary must not create a managed layout outside the official image")
	}
	if _, err := os.Stat(filepath.Join(rt, "bin")); !os.IsNotExist(err) {
		t.Fatalf("bin should not be created: %v", err)
	}
	if len(sum.Catalog) == 0 {
		t.Fatal("catalog should remain readable")
	}
	for _, pkg := range sum.Catalog {
		if pkg.Supported {
			t.Fatalf("catalog package must be non-installable outside official image: %+v", pkg)
		}
	}
}

func TestServiceSummaryDoesNotInitializeLayoutTwiceOutsideOfficialImage(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	rt := filepath.Join(data, "runtime")
	svc := NewService(
		func() Policy { return DefaultPolicy() },
		func() string { return data },
		func() string { return rt },
	)
	svc.supportFn = func() RuntimeManagementSupport {
		return RuntimeManagementSupport{Reason: "test unsupported"}
	}
	_ = svc.Summary()
	if svc.layoutDir != "" {
		t.Fatalf("layout marker=%q, want empty outside official image", svc.layoutDir)
	}
	_ = svc.Summary()
	if _, err := os.Stat(rt); !os.IsNotExist(err) {
		t.Fatalf("summary must not initialize runtime directory, stat err=%v", err)
	}
}

func TestServiceRejectsManagedActionsWithoutWritingRuntime(t *testing.T) {
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "runtime")
	svc := NewService(
		func() Policy { return DefaultPolicy() },
		func() string { return root },
		func() string { return runtimeDir },
	)
	svc.supportFn = func() RuntimeManagementSupport {
		return RuntimeManagementSupport{Reason: "test unsupported"}
	}

	actions := []struct {
		name string
		call func() error
	}{
		{
			name: "preview install",
			call: func() error {
				_, err := svc.PreviewInstall(DefaultNodePackageID)
				return err
			},
		},
		{
			name: "install package",
			call: func() error {
				_, err := svc.InstallPackage(context.Background(), DefaultNodePackageID)
				return err
			},
		},
		{
			name: "uninstall package",
			call: func() error { return svc.UninstallPackage(DefaultNodePackageID) },
		},
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
			if err := action.call(); !errors.Is(err, ErrManagedRuntimeUnsupported) {
				t.Fatalf("error=%v, expected ErrManagedRuntimeUnsupported", err)
			}
			if _, err := os.Stat(runtimeDir); !os.IsNotExist(err) {
				t.Fatalf("unsupported action created runtime directory: %v", err)
			}
		})
	}
}
