package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectDirectoryLaunchConvention(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.py"), []byte("print(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := InspectDirectoryLaunch(root, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Command != "python3" {
		t.Fatalf("%+v", res)
	}
	if !filepath.IsAbs(res.Entries[0].Args[0]) {
		t.Fatalf("entry should be absolute: %+v", res.Entries[0])
	}
}

func TestInspectDirectoryLaunchManifestAndEscape(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":1,"entries":[{"id":"main","command":"node","args":["index.js"],"cwd":"."}]}`
	if err := os.WriteFile(filepath.Join(root, DirectoryLaunchManifest), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := InspectDirectoryLaunch(root, DefaultPolicy())
	if err != nil || len(res.Entries) != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}

	bad := `{"version":1,"entries":[{"id":"bad","command":"node","args":["../evil.js"],"cwd":"."}]}`
	if err := os.WriteFile(filepath.Join(root, DirectoryLaunchManifest), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectDirectoryLaunch(root, DefaultPolicy()); err == nil {
		t.Fatal("escape should fail")
	}
}

func TestInspectDirectoryLaunchRejectsOversizedManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, DirectoryLaunchManifest), make([]byte, 256*1024+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectDirectoryLaunch(root, DefaultPolicy()); err == nil {
		t.Fatal("oversized manifest should fail")
	}
}

func TestInspectDirectoryLaunchRejectsShell(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":1,"entries":[{"id":"bad","command":"bash","args":["x.py"],"cwd":"."}]}`
	if err := os.WriteFile(filepath.Join(root, DirectoryLaunchManifest), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectDirectoryLaunch(root, DefaultPolicy()); err == nil {
		t.Fatal("shell should fail")
	}
}

func TestInspectDirectoryLaunchManifestSkipsInvalidEntries(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := "{\"version\":1,\"entries\":[{\"id\":\"ok\",\"command\":\"node\",\"args\":[\"index.js\"]},{\"id\":\"bad\",\"command\":\"bash\",\"args\":[\"index.js\"]},{\"id\":\"duplicate\",\"command\":\"node\",\"args\":[\"index.js\"]},{\"id\":\"duplicate\",\"command\":\"node\",\"args\":[\"index.js\"]}]}"
	if err := os.WriteFile(filepath.Join(root, DirectoryLaunchManifest), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := InspectDirectoryLaunch(root, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 || res.Entries[0].ID != "ok" || res.Entries[1].ID != "duplicate" {
		t.Fatalf("entries=%+v", res.Entries)
	}
	if len(res.Warnings) != 2 {
		t.Fatalf("warnings=%v", res.Warnings)
	}
}
