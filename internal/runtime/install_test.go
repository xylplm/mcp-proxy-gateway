package runtime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSafeJoinRejectsTraversal(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	if _, err := safeJoin(base, "../etc/passwd"); err == nil {
		t.Fatal("expected traversal reject")
	}
	if _, err := safeJoin(base, "ok/file.txt"); err != nil {
		t.Fatalf("ok path: %v", err)
	}
}

func TestReplaceStagedTreeKeepsPreviousTargetOnFailure(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "node")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(target, "version.txt")
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := replaceStagedTree(filepath.Join(root, "missing.staging"), target)
	if err == nil {
		t.Fatal("expected replacement failure")
	}
	got, readErr := os.ReadFile(oldFile)
	if readErr != nil {
		t.Fatalf("previous target should be restored: %v", readErr)
	}
	if string(got) != "old" {
		t.Fatalf("previous target content=%q, want old", got)
	}
	if _, statErr := os.Stat(filepath.Join(root, "missing.staging")); !os.IsNotExist(statErr) {
		t.Fatalf("staging should be cleaned, stat err=%v", statErr)
	}
	matches, globErr := filepath.Glob(filepath.Join(root, "node.backup-*"))
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("backup should not remain after successful rollback: %v", matches)
	}
}

func TestReplaceStagedTreeReplacesOnlyAfterStagingIsReady(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "uv")
	staging := filepath.Join(root, "uv.staging")
	if err := os.MkdirAll(filepath.Join(target, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "bin", "uv"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(staging, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "bin", "uv"), []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replaceStagedTree(staging, target); err != nil {
		t.Fatalf("replace staged tree: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(target, "bin", "uv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("target content=%q, want new", got)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("staging should be removed after success, stat err=%v", err)
	}
}

func TestInstallUVFromFakeServer(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("#!/bin/sh\necho uv-mock\n")
	hdr := &tar.Header{Name: "uv", Mode: 0o755, Size: int64(len(content))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	content2 := []byte("#!/bin/sh\necho uvx-mock\n")
	hdr2 := &tar.Header{Name: "uvx", Mode: 0o755, Size: int64(len(content2))}
	if err := tw.WriteHeader(hdr2); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content2); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	payload := buf.Bytes()
	sum := sha256.Sum256(payload)
	sha := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)

	rt := t.TempDir()
	if err := EnsureRuntimeLayout(rt); err != nil {
		t.Fatal(err)
	}
	in := NewInstaller(rt, srv.Client())
	in.catalog = func() []PackageSpec {
		return []PackageSpec{{
			ID:      "uv-test",
			Name:    "uv-test",
			Version: "0.0.1",
			Kind:    PackageKindUV,
			Tools:   []string{"uv", "uvx"},
			Assets: []PackageAsset{{
				GOOS:   runtime.GOOS,
				GOARCH: runtime.GOARCH,
				URL:    srv.URL + "/uv.tar.gz",
				SHA256: sha,
				Format: "tar.gz",
			}},
		}}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// checksum failure
	tmp := filepath.Join(rt, "cache", "t.tgz")
	_ = os.MkdirAll(filepath.Dir(tmp), 0o755)
	if err := in.downloadFile(ctx, srv.URL+"/uv.tar.gz", tmp+".bad", strings.Repeat("0", 64)); err == nil {
		t.Fatal("expected checksum failure")
	}

	res, err := in.Install(ctx, "uv-test")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.ID != "uv-test" || res.Reused {
		t.Fatalf("result=%+v", res)
	}
	if _, err := os.Stat(filepath.Join(rt, "uv", "bin", "uv")); err != nil {
		t.Fatalf("uv missing in managed uv directory: %v", err)
	}
	// 二次安装应 reuse
	res2, err := in.Install(ctx, "uv-test")
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if !res2.Reused {
		t.Fatalf("expected reuse, got %+v", res2)
	}

	if err := in.Uninstall("uv-test"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
}

func TestRestoreNodeLaunchersFromSkippedSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Node tar launcher test requires Unix symlinks")
	}
	root := filepath.Join(t.TempDir(), "node-v24.19.0-linux-x64")
	if err := os.MkdirAll(filepath.Join(root, "lib", "node_modules", "npm", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"npm-cli.js", "npx-cli.js"} {
		if err := os.WriteFile(filepath.Join(root, "lib", "node_modules", "npm", "bin", name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := restoreNodeLaunchers(root); err != nil {
		t.Fatalf("restore launchers: %v", err)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		if _, ok := findExecutableInDir(filepath.Join(root, "bin"), name); !ok {
			t.Fatalf("launcher %s missing", name)
		}
	}
}

func TestFindPackageAndSelectAsset(t *testing.T) {
	t.Parallel()
	spec, ok := FindPackage(DefaultNodePackageID)
	if !ok || spec.Kind != PackageKindNode {
		t.Fatalf("default node package missing")
	}
	if spec.Version != "24.19.0" {
		t.Fatalf("default node version=%q", spec.Version)
	}
	if _, ok := FindPackage("node-22.14.0"); !ok {
		t.Fatal("legacy node package should remain available")
	}
	if _, ok := FindPackage("nope"); ok {
		t.Fatal("unknown should miss")
	}
	if _, ok := SelectAsset(spec, "linux", "amd64"); !ok {
		t.Fatal("linux/amd64 asset required")
	}
}

func TestCatalogWithStatus(t *testing.T) {
	rt := t.TempDir()
	_ = EnsureRuntimeLayout(rt)
	in := NewInstaller(rt, nil)
	cat := in.CatalogWithStatus()
	if len(cat) == 0 {
		t.Fatal("empty catalog")
	}
	for _, p := range cat {
		if p.ID == "" || p.Name == "" || len(p.Tools) == 0 {
			t.Fatalf("catalog item missing public fields: %+v", p)
		}
	}
}

func TestInstallerProgressSnapshotIsCopied(t *testing.T) {
	in := NewInstaller(t.TempDir(), nil)
	in.setProgress(&InstallProgress{PackageID: "uv-0.6.14", Phase: "downloading", Bytes: 10, Total: 20})
	progress := in.currentProgress()
	if progress == nil || progress.PackageID != "uv-0.6.14" || progress.Bytes != 10 {
		t.Fatalf("progress=%+v", progress)
	}
	progress.Bytes = 999
	if got := in.currentProgress().Bytes; got != 10 {
		t.Fatalf("progress snapshot is mutable: %d", got)
	}
	in.setProgress(nil)
	if in.currentProgress() != nil {
		t.Fatal("progress should clear after installation")
	}
}

// TestInstallerLogsCaptureSuccess 验证成功安装会写入包含 success 级别的日志。
func TestInstallerLogsCaptureSuccess(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("#!/bin/sh\necho uv\n")
	if err := tw.WriteHeader(&tar.Header{Name: "uv", Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	content2 := []byte("#!/bin/sh\necho uvx\n")
	if err := tw.WriteHeader(&tar.Header{Name: "uvx", Mode: 0o755, Size: int64(len(content2))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content2); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	payload := buf.Bytes()
	sum := sha256.Sum256(payload)
	sha := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)

	rt := t.TempDir()
	if err := EnsureRuntimeLayout(rt); err != nil {
		t.Fatal(err)
	}
	in := NewInstaller(rt, srv.Client())
	in.catalog = func() []PackageSpec {
		return []PackageSpec{{
			ID: "uv-log", Name: "uv-log", Version: "0.0.1", Kind: PackageKindUV,
			Tools:  []string{"uv", "uvx"},
			Assets: []PackageAsset{{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, URL: srv.URL + "/uv.tar.gz", SHA256: sha, Format: "tar.gz"}},
		}}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := in.Install(ctx, "uv-log"); err != nil {
		t.Fatalf("install: %v", err)
	}

	logs := in.Logs()
	if len(logs) == 0 {
		t.Fatal("expected install logs")
	}
	// 应包含开始、下载成功、安装完成三类。
	var hasInfo, hasDownloadSuccess, hasInstallSuccess bool
	for _, e := range logs {
		if e.Level == InstallLogInfo {
			hasInfo = true
		}
		if e.Phase == "downloading" && e.Level == InstallLogSuccess {
			hasDownloadSuccess = true
		}
		if e.Phase == "placing" && e.Level == InstallLogSuccess && strings.Contains(e.Message, "安装完成") {
			hasInstallSuccess = true
		}
	}
	if !hasInfo {
		t.Fatalf("missing info log: %+v", logs)
	}
	if !hasDownloadSuccess {
		t.Fatalf("missing download success log: %+v", logs)
	}
	if !hasInstallSuccess {
		t.Fatalf("missing install success log: %+v", logs)
	}
	// 成功后无残留错误。
	if in.lastInstallError() != "" {
		t.Fatalf("unexpected lastError=%q", in.lastInstallError())
	}
}

// TestInstallerLogsCaptureError 验证失败安装写入 error 日志并保留 lastError。
func TestInstallerLogsCaptureError(t *testing.T) {
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(official.Close)

	rt := t.TempDir()
	if err := EnsureRuntimeLayout(rt); err != nil {
		t.Fatal(err)
	}
	in := NewInstaller(rt, official.Client())
	in.catalog = func() []PackageSpec {
		return []PackageSpec{{
			ID: "uv-err", Name: "uv-err", Version: "0.0.1", Kind: PackageKindUV,
			Tools: []string{"uv", "uvx"},
			Assets: []PackageAsset{{
				GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
				URL: official.URL + "/uv.tar.gz", SHA256: strings.Repeat("a", 64), Format: "tar.gz",
			}},
		}}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := in.Install(ctx, "uv-err")
	if err == nil {
		t.Fatal("expected install failure")
	}
	if in.lastInstallError() == "" {
		t.Fatal("expected lastError to be persisted")
	}
	if !strings.Contains(in.lastInstallError(), "所有下载源均失败") {
		t.Fatalf("lastError=%q", in.lastInstallError())
	}
	var hasError bool
	for _, e := range in.Logs() {
		if e.Level == InstallLogError {
			hasError = true
		}
	}
	if !hasError {
		t.Fatal("expected at least one error log")
	}
}

// TestInstallerLogsRingBuffer 验证日志环形缓冲上限。
func TestInstallerLogsRingBuffer(t *testing.T) {
	in := NewInstaller(t.TempDir(), nil)
	for i := 0; i < maxInstallLogs+50; i++ {
		in.addLog(InstallLogEntry{Phase: "test", Level: InstallLogInfo, Message: fmt.Sprintf("entry-%d", i)})
	}
	logs := in.Logs()
	if len(logs) != maxInstallLogs {
		t.Fatalf("ring buffer size=%d, want %d", len(logs), maxInstallLogs)
	}
	// 应保留最新的 maxInstallLogs 条（即 entry-50 起）。
	first := logs[0].Message
	if first != "entry-50" {
		t.Fatalf("oldest kept=%q, want entry-50", first)
	}
}

// TestInstallerLogsSnapshotIsCopied 验证 Logs() 返回副本，外部修改不影响内部。
func TestInstallerLogsSnapshotIsCopied(t *testing.T) {
	in := NewInstaller(t.TempDir(), nil)
	in.addLog(InstallLogEntry{Phase: "x", Level: InstallLogInfo, Message: "m"})
	logs := in.Logs()
	logs[0].Message = "tampered"
	if in.Logs()[0].Message != "m" {
		t.Fatal("Logs() should return a copy")
	}
}

func TestPreviewUnknownPackage(t *testing.T) {
	in := NewInstaller(t.TempDir(), nil)
	_, err := in.PreviewInstall("not-exist")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "未知") {
		t.Fatalf("msg=%v", err)
	}
}

func TestInstallRejectsEmptyRuntimeDir(t *testing.T) {
	in := NewInstaller("", nil)
	_, err := in.Install(context.Background(), "uv-0.6.14")
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestDownloadWithFallbackFallsThroughMirrors 验证官方源失败后回退到镜像源。
func TestDownloadWithFallbackFallsThroughMirrors(t *testing.T) {
	payload := []byte("mirror-fallback-payload")
	sum := sha256.Sum256(payload)
	sha := hex.EncodeToString(sum[:])

	// 第二个服务器（镜像）返回正确内容；第一个（官方）返回 503。
	var goodSrv *httptest.Server
	goodSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(goodSrv.Close)

	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(badSrv.Close)

	rt := t.TempDir()
	in := NewInstaller(rt, goodSrv.Client())
	dest := filepath.Join(rt, "out.bin")

	sources := []downloadSource{
		{url: badSrv.URL + "/official", mirror: false},
		{url: goodSrv.URL + "/mirror", mirror: true},
	}
	if err := in.downloadWithFallback(context.Background(), sources, dest, sha); err != nil {
		t.Fatalf("expected fallback success, got: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("content mismatch")
	}
}

// TestDownloadWithFallbackAllFailAggregatesErrors 验证全部源失败时返回聚合错误。
func TestDownloadWithFallbackAllFailAggregatesErrors(t *testing.T) {
	bad1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(bad1.Close)
	bad2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	t.Cleanup(bad2.Close)

	rt := t.TempDir()
	in := NewInstaller(rt, bad1.Client())
	dest := filepath.Join(rt, "out.bin")
	sources := []downloadSource{
		{url: bad1.URL + "/official", mirror: false},
		{url: bad2.URL + "/mirror", mirror: true},
	}
	err := in.downloadWithFallback(context.Background(), sources, dest, strings.Repeat("a", 64))
	if err == nil {
		t.Fatal("expected aggregate error")
	}
	if !strings.Contains(err.Error(), "所有下载源均失败") {
		t.Fatalf("missing aggregate prefix: %v", err)
	}
	if !strings.Contains(err.Error(), "官方源") || !strings.Contains(err.Error(), "镜像源") {
		t.Fatalf("missing source labels: %v", err)
	}
}

// TestDownloadWithFallbackSHA256MismatchFails 验证 SHA256 校验失败不视为「成功源」。
func TestDownloadWithFallbackSHA256MismatchFails(t *testing.T) {
	payload := []byte("tampered")
	sum := sha256.Sum256(payload)
	sha := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// 写入与 sha 不匹配的内容
		_, _ = w.Write([]byte("different"))
	}))
	t.Cleanup(srv.Close)

	rt := t.TempDir()
	in := NewInstaller(rt, srv.Client())
	dest := filepath.Join(rt, "out.bin")
	sources := []downloadSource{{url: srv.URL + "/x", mirror: false}}
	err := in.downloadWithFallback(context.Background(), sources, dest, sha)
	if err == nil {
		t.Fatal("expected checksum error")
	}
	if !strings.Contains(err.Error(), "校验和不匹配") {
		t.Fatalf("expected checksum mismatch, got: %v", err)
	}
}

// TestInstallUsesMirrorWhenOfficialFails 端到端：官方源 503，镜像源提供真实 uv tarball，安装成功。
func TestInstallUsesMirrorWhenOfficialFails(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("#!/bin/sh\necho uv\n")
	if err := tw.WriteHeader(&tar.Header{Name: "uv", Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	content2 := []byte("#!/bin/sh\necho uvx\n")
	if err := tw.WriteHeader(&tar.Header{Name: "uvx", Mode: 0o755, Size: int64(len(content2))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content2); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	payload := buf.Bytes()
	sum := sha256.Sum256(payload)
	sha := hex.EncodeToString(sum[:])

	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(mirror.Close)
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(official.Close)

	rt := t.TempDir()
	if err := EnsureRuntimeLayout(rt); err != nil {
		t.Fatal(err)
	}
	in := NewInstaller(rt, mirror.Client())
	in.catalog = func() []PackageSpec {
		return []PackageSpec{{
			ID: "uv-mirror", Name: "uv-mirror", Version: "0.0.1", Kind: PackageKindUV,
			Tools: []string{"uv", "uvx"},
			Assets: []PackageAsset{{
				GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
				URL:     official.URL + "/official.tar.gz",
				SHA256:  sha,
				Format:  "tar.gz",
				Mirrors: []MirrorAsset{{URL: mirror.URL + "/mirror.tar.gz"}},
			}},
		}}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := in.Install(ctx, "uv-mirror")
	if err != nil {
		t.Fatalf("install via mirror: %v", err)
	}
	if res.Reused {
		t.Fatalf("expected fresh install")
	}
	if _, err := os.Stat(filepath.Join(rt, "uv", "bin", "uv")); err != nil {
		t.Fatalf("uv missing after mirror install: %v", err)
	}
}
