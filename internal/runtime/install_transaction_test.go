package runtime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallRestoresPreviousRuntimeWhenStateWriteFails(t *testing.T) {
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
	oldUV := filepath.Join(runtimeDir, RuntimeSubdirUV, "bin", "uv")
	if err := os.MkdirAll(filepath.Dir(oldUV), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldUV, []byte("#!/bin/sh\necho old-uv\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	in := NewInstaller(runtimeDir, server.Client())
	spec := PackageSpec{
		ID:      "uv-transaction-test",
		Name:    "uv",
		Version: "test",
		Kind:    PackageKindUV,
		Tools:   []string{"uv", "uvx"},
		Assets: []PackageAsset{{
			GOOS:   runtime.GOOS,
			GOARCH: runtime.GOARCH,
			URL:    server.URL + "/uv.tar.gz",
			SHA256: hex.EncodeToString(sum[:]),
			Format: "tar.gz",
		}},
	}
	in.catalog = func() []PackageSpec { return []PackageSpec{spec} }
	if err := in.saveState(InstallState{Packages: []InstallRecord{{
		ID: spec.ID, Name: spec.Name, Version: spec.Version, Kind: string(spec.Kind), Tools: spec.Tools,
	}}}); err != nil {
		t.Fatal(err)
	}
	in.saveStateFn = func(InstallState) error { return errors.New("state storage unavailable") }

	if _, err := in.Install(context.Background(), spec.ID); err == nil {
		t.Fatal("install should fail when state persistence fails")
	}
	contents, err := os.ReadFile(oldUV)
	if err != nil {
		t.Fatalf("previous uv executable was not restored: %v", err)
	}
	if !bytes.Contains(contents, []byte("old-uv")) {
		t.Fatalf("uv contents=%q, want previous runtime", contents)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, RuntimeSubdirUV, "bin", "uvx")); !os.IsNotExist(err) {
		t.Fatalf("new uvx should have been rolled back, stat err=%v", err)
	}
	state := in.loadState()
	if installed, _ := state.find(spec.ID); !installed {
		t.Fatal("previous install record should remain after install rollback")
	}
}

func runtimeTestTarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, body := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
