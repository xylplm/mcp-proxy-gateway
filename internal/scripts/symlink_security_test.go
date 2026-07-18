package scripts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveEntryRejectsTamperedContent(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	detail, err := svc.Create(CreateInput{Name: "tamper-demo", Language: LangPython, Content: "print(1)\n"})
	if err != nil {
		t.Fatal(err)
	}
	entry := detail.EntryPath
	if err := os.WriteFile(entry, []byte("print('changed')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := svc.BuildLaunchBinding(detail.ID, detail.CurrentVersion); err == nil {
		t.Fatal("tampered script should not resolve")
	}
}

func TestReadAPIsRejectSymlinkOutsideLibrary(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	detail, err := svc.Create(CreateInput{Name: "read-link-demo", Language: LangPython, Content: "print(1)\n"})
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.py")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(detail.EntryPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, detail.EntryPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := svc.Get(detail.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.GetDetail(detail.ID); err == nil || strings.Contains(got.Content, "SECRET") {
		t.Fatalf("detail must reject outside symlink, got=%q err=%v", got.Content, err)
	}
	if got, _, err := svc.GetVersion(detail.ID, detail.CurrentVersion); err == nil || strings.Contains(got, "SECRET") {
		t.Fatalf("version must reject outside symlink, got=%q err=%v", got, err)
	}
}

func TestReadAPIsRejectTamperedEntryMetadata(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	detail, err := svc.Create(CreateInput{Name: "meta-demo", Language: LangPython, Content: "print(1)\n"})
	if err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(LibraryRoot(dir), detail.ID, "meta.json")
	b, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(b, &meta); err != nil {
		t.Fatal(err)
	}
	meta["entryFile"] = "../../secret.py"
	b, err = json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metaPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetDetail(detail.ID); err == nil {
		t.Fatal("tampered entry metadata must fail")
	}
}

func TestReadAPIsRejectExternallyEnlargedContent(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	detail, err := svc.Create(CreateInput{Name: "large-demo", Language: LangPython, Content: "print(1)\n"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(detail.EntryPath, make([]byte, MaxScriptBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetDetail(detail.ID); err == nil {
		t.Fatal("externally enlarged content must fail")
	}
}

func TestResolveEntryRejectsSymlinkOutsideLibrary(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	detail, err := svc.Create(CreateInput{Name: "link-demo", Language: LangPython, Content: "print(1)\n"})
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "evil.py")
	if err := os.WriteFile(outside, []byte("print(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(detail.EntryPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, detail.EntryPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, _, _, _, err := svc.BuildLaunchBinding(detail.ID, detail.CurrentVersion); err == nil {
		t.Fatal("symlink outside library should fail")
	}
}
