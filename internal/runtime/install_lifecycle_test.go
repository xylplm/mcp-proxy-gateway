package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUninstallPreservesNpmDependencies(t *testing.T) {
	runtimeDir := t.TempDir()
	if err := EnsureRuntimeLayout(runtimeDir); err != nil {
		t.Fatal(err)
	}
	in := NewInstaller(runtimeDir, nil)
	spec := PackageSpec{ID: "node-test", Name: "Node", Version: "test", Kind: PackageKindNode}
	in.catalog = func() []PackageSpec { return []PackageSpec{spec} }
	if err := os.WriteFile(filepath.Join(runtimeDir, RuntimeSubdirNode, "marker"), []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	npmPackage := filepath.Join(runtimeDir, RuntimeSubdirNpm, "node_modules", "example", "package.json")
	if err := os.MkdirAll(filepath.Dir(npmPackage), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(npmPackage, []byte(`{"name":"example"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state := InstallState{Packages: []InstallRecord{{ID: spec.ID, Name: spec.Name, Version: spec.Version, Kind: string(spec.Kind)}}}
	if err := in.saveState(state); err != nil {
		t.Fatal(err)
	}

	if err := in.Uninstall(spec.ID); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(npmPackage); err != nil {
		t.Fatalf("npm dependency should survive Node uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, RuntimeSubdirNode)); !os.IsNotExist(err) {
		t.Fatalf("managed Node directory should be removed: %v", err)
	}
}

func TestUninstallPreservesUnrecordedManagedDirectory(t *testing.T) {
	runtimeDir := t.TempDir()
	if err := EnsureRuntimeLayout(runtimeDir); err != nil {
		t.Fatal(err)
	}
	in := NewInstaller(runtimeDir, nil)
	spec := PackageSpec{ID: "node-test", Name: "Node", Version: "test", Kind: PackageKindNode}
	in.catalog = func() []PackageSpec { return []PackageSpec{spec} }
	manualFile := filepath.Join(runtimeDir, RuntimeSubdirNode, "manual")
	if err := os.WriteFile(manualFile, []byte("manual"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := in.Uninstall(spec.ID)
	if err == nil || !strings.Contains(err.Error(), "尚未由受管安装器安装") {
		t.Fatalf("Uninstall error=%v", err)
	}
	if _, err := os.Stat(manualFile); err != nil {
		t.Fatalf("unrecorded managed directory was removed: %v", err)
	}
}

func TestUninstallRestoresDirectoryWhenStateWriteFails(t *testing.T) {
	runtimeDir := t.TempDir()
	if err := EnsureRuntimeLayout(runtimeDir); err != nil {
		t.Fatal(err)
	}
	in := NewInstaller(runtimeDir, nil)
	spec := PackageSpec{ID: "uv-test", Name: "uv", Version: "test", Kind: PackageKindUV}
	in.catalog = func() []PackageSpec { return []PackageSpec{spec} }
	managedFile := filepath.Join(runtimeDir, RuntimeSubdirUV, "bin", "uv")
	if err := os.MkdirAll(filepath.Dir(managedFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedFile, []byte("managed"), 0o755); err != nil {
		t.Fatal(err)
	}
	state := InstallState{Packages: []InstallRecord{{ID: spec.ID, Name: spec.Name, Version: spec.Version, Kind: string(spec.Kind)}}}
	if err := in.saveState(state); err != nil {
		t.Fatal(err)
	}
	in.saveStateFn = func(InstallState) error { return errors.New("state write failed") }

	err := in.Uninstall(spec.ID)
	if err == nil || !strings.Contains(err.Error(), "写入卸载状态失败") {
		t.Fatalf("Uninstall error=%v", err)
	}
	if _, err := os.Stat(managedFile); err != nil {
		t.Fatalf("managed directory was not restored: %v", err)
	}
	persistedState := in.loadState()
	if installed, _ := persistedState.find(spec.ID); !installed {
		t.Fatal("persisted install record should remain after rollback")
	}
}

func TestInstallPreservesUnrecordedRuntimeDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake archive test requires Unix-compatible runtime layout")
	}
	payload := runtimeTestTarGz(t, map[string][]byte{
		"uv":  []byte("#!/bin/sh\necho new-uv\n"),
		"uvx": []byte("#!/bin/sh\necho new-uvx\n"),
	})
	sum := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)

	runtimeDir := t.TempDir()
	if err := EnsureRuntimeLayout(runtimeDir); err != nil {
		t.Fatal(err)
	}
	in := NewInstaller(runtimeDir, server.Client())
	spec := PackageSpec{
		ID: "uv-manual", Name: "uv", Version: "test", Kind: PackageKindUV, Tools: []string{"uv", "uvx"},
		Assets: []PackageAsset{{
			GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, URL: server.URL + "/uv.tar.gz",
			SHA256: hex.EncodeToString(sum[:]), Format: "tar.gz",
		}},
	}
	in.catalog = func() []PackageSpec { return []PackageSpec{spec} }
	manualFile := filepath.Join(runtimeDir, RuntimeSubdirUV, "manual")
	if err := os.WriteFile(manualFile, []byte("manual"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := in.Install(context.Background(), spec.ID)
	if err == nil || !strings.Contains(err.Error(), "未记录") {
		t.Fatalf("Install error=%v", err)
	}
	if _, err := os.Stat(manualFile); err != nil {
		t.Fatalf("manual runtime content was overwritten: %v", err)
	}
}

func TestPackageToolsPresentRequiresManagedDirectory(t *testing.T) {
	runtimeDir := t.TempDir()
	in := NewInstaller(runtimeDir, nil)
	spec := PackageSpec{Kind: PackageKindNode, Tools: []string{"node"}}
	if in.packageToolsPresent(spec) {
		t.Fatal("system PATH must not satisfy a missing managed Node directory")
	}
}

// 历史版本的安装器会把 uv/uvx 副本写进 runtime/bin，而 runtime/bin 在 PATH 上排在
// 受管目录之前。升级后必须能识别出这类遮蔽，否则新版本会被静默忽略。
func TestShadowedToolsDetectsLegacyBinOverride(t *testing.T) {
	runtimeDir := t.TempDir()
	if err := EnsureRuntimeLayout(runtimeDir); err != nil {
		t.Fatal(err)
	}
	spec := PackageSpec{Kind: PackageKindUV, Tools: []string{"uv", "uvx"}}
	managedBin := filepath.Join(runtimeDir, RuntimeSubdirUV, "bin")
	if err := os.MkdirAll(managedBin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range spec.Tools {
		if err := os.WriteFile(filepath.Join(managedBin, tool), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	legacyShim := filepath.Join(runtimeDir, RuntimeSubdirBin, "uv")
	if err := os.WriteFile(legacyShim, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	invalidatePathPrefixes(runtimeDir)

	in := NewInstaller(runtimeDir, nil)
	shadowed := in.shadowedTools(spec)
	if len(shadowed) != 1 {
		t.Fatalf("shadowed=%+v, want only the overridden uv", shadowed)
	}
	if shadowed[0].tool != "uv" || shadowed[0].path != legacyShim {
		t.Fatalf("shadowed=%+v, want uv at %q", shadowed[0], legacyShim)
	}

	if err := os.Remove(legacyShim); err != nil {
		t.Fatal(err)
	}
	invalidatePathPrefixes(runtimeDir)
	if shadowed := in.shadowedTools(spec); len(shadowed) != 0 {
		t.Fatalf("managed tools must not report shadowing once bin is clean: %+v", shadowed)
	}
}
