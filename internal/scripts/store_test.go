package scripts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSoftDeleteReportsTrashMoveFailure(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	detail, err := store.Create(CreateInput{Name: "move-failure", Language: LangPython, Content: "print(1)\n"})
	if err != nil {
		t.Fatal(err)
	}
	store.rename = func(string, string) error { return errors.New("simulated rename failure") }
	err = store.SoftDelete(detail.ID)
	if !errors.Is(err, ErrTrashMoveFailed) {
		t.Fatalf("error=%v, want ErrTrashMoveFailed", err)
	}
	if _, statErr := os.Stat(filepath.Join(LibraryRoot(dir), detail.ID)); statErr != nil {
		t.Fatalf("library directory should remain for cleanup retry: %v", statErr)
	}
	list, listErr := store.List()
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(list) != 0 {
		t.Fatalf("trash script should not appear in active list: %+v", list)
	}
}

func TestCreateListGetSaveVersion(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)

	detail, err := svc.Create(CreateInput{
		Name:     "demo-echo",
		Language: LangPython,
		Content:  "print('hello')\n",
		Tags:     []string{"demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if detail.ID == "" || detail.CurrentVersion != "v1" || detail.Runtime != "python3" {
		t.Fatalf("%+v", detail)
	}
	if detail.Risk.Level == "" {
		t.Fatal("risk missing")
	}

	list, err := svc.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%v err=%v", list, err)
	}

	d2, err := svc.SaveContent(detail.ID, SaveContentInput{Content: "import os\nos.system('x')\n", Note: "riskier"})
	if err != nil {
		t.Fatal(err)
	}
	if d2.CurrentVersion != "v2" {
		t.Fatalf("version=%s", d2.CurrentVersion)
	}
	if d2.Risk.Level != RiskCritical && d2.Risk.Level != RiskHigh {
		t.Fatalf("expected elevated risk, got %+v", d2.Risk)
	}

	vers, err := svc.ListVersions(detail.ID)
	if err != nil || len(vers) < 2 {
		t.Fatalf("vers=%v err=%v", vers, err)
	}

	// 回切 v1
	sc, err := svc.ActivateVersion(detail.ID, "v1")
	if err != nil || sc.CurrentVersion != "v1" {
		t.Fatalf("%+v %v", sc, err)
	}
	// 回切后再保存必须分配 v3，不能覆盖已存在 v2。
	d3, err := svc.SaveContent(detail.ID, SaveContentInput{Content: "print(3)\n", Note: "v3"})
	if err != nil || d3.CurrentVersion != "v3" {
		t.Fatalf("save after rollback: %+v err=%v", d3, err)
	}

	bind, cmd, args, cwd, err := svc.BuildLaunchBinding(detail.ID, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "python3" || len(args) != 1 || !strings.Contains(args[0], "main.py") || cwd == "" {
		t.Fatalf("cmd=%s args=%v cwd=%s bind=%+v", cmd, args, cwd, bind)
	}

	if err := svc.Delete(detail.ID); err != nil {
		t.Fatal(err)
	}
	list, err = svc.List()
	if err != nil || len(list) != 0 {
		t.Fatalf("after delete list=%v err=%v", list, err)
	}
	// 软删后业务读路径必须 fail-closed，避免已删除脚本仍可被编辑/启动。
	if _, err := svc.Get(detail.ID); err == nil {
		t.Fatal("get after delete should fail")
	}
	if _, err := svc.GetDetail(detail.ID); err == nil {
		t.Fatal("detail after delete should fail")
	}
	if _, _, _, _, err := svc.BuildLaunchBinding(detail.ID, "v1"); err == nil {
		t.Fatal("launch after delete should fail")
	}
	if _, err := svc.ListVersions(detail.ID); err == nil {
		t.Fatal("versions after delete should fail")
	}
}

func TestAnalyzeAndValidate(t *testing.T) {
	if err := ValidateScriptName(""); err == nil {
		t.Fatal("empty name")
	}
	if err := ValidateContent(strings.Repeat("a", MaxScriptBytes+1)); err == nil {
		t.Fatal("too large")
	}
	r := AnalyzeContent("const x = fetch('http://x')\n")
	if r.Level == RiskLow && r.Score == 0 {
		t.Fatalf("network should raise risk: %+v", r)
	}
	diff := DiffText("a", "line1\nline2\n", "b", "line1\nline3\n")
	if len(diff.Hunks) == 0 {
		t.Fatal("expected hunks")
	}
}

func TestRejectShellRuntime(t *testing.T) {
	if _, err := NormalizeRuntime("bash", LangPython); err == nil {
		t.Fatal("bash must fail")
	}
	if _, err := NormalizeRuntime("../node", LangJavaScript); err == nil {
		t.Fatal("path runtime must fail")
	}
}
