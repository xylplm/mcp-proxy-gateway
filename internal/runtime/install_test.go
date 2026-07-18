package runtime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	if _, err := os.Stat(filepath.Join(rt, "bin", "uv")); err != nil {
		t.Fatalf("uv missing in bin: %v", err)
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

func TestFindPackageAndSelectAsset(t *testing.T) {
	t.Parallel()
	spec, ok := FindPackage("node-22.14.0")
	if !ok || spec.Kind != PackageKindNode {
		t.Fatalf("node package missing")
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
		if p.ID == "" || p.Name == "" {
			t.Fatalf("invalid item: %+v", p)
		}
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
