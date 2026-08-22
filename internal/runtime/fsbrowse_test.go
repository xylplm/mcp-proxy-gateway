package runtime

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestNormalizeBrowsePath(t *testing.T) {
	t.Parallel()
	if _, err := NormalizeBrowsePath(""); err == nil {
		t.Fatal("empty should fail")
	}
	if _, err := NormalizeBrowsePath("relative/path"); err == nil {
		t.Fatal("relative should fail")
	}
	got, err := NormalizeBrowsePath("/data/workspaces/../workspaces/demo")
	if err != nil {
		t.Fatal(err)
	}
	// Unix 风格路径统一为正斜杠逻辑路径，跨平台一致。
	if got != "/data/workspaces/demo" {
		t.Fatalf("got %q want %q", got, "/data/workspaces/demo")
	}
	// 真实本地绝对路径
	local := t.TempDir()
	got, err = NormalizeBrowsePath(local)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(local) && filepath.ToSlash(got) != filepath.ToSlash(filepath.Clean(local)) {
		t.Fatalf("local normalize: got %q local %q", got, local)
	}
}

func TestPathUnderAnyRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	child := filepath.Join(root, "ws", "a")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(root), "outside-"+filepath.Base(root))
	if !PathUnderAnyRoot(child, []string{root}) {
		t.Fatalf("child %q should be under root %q", child, root)
	}
	if PathUnderAnyRoot(outside, []string{root}) {
		t.Fatal("outside should not be under root")
	}
	if !PathUnderAnyRoot(root, []string{root}) {
		t.Fatal("root should contain itself")
	}
	// Unix 风格逻辑路径
	if !PathUnderAnyRoot("/data/ws/a", []string{"/data"}) {
		t.Fatal("unix-style child should be under root")
	}
	if PathUnderAnyRoot("/etc", []string{"/data"}) {
		t.Fatal("unix-style outside should not be under root")
	}
	if PathUnderAnyRoot("/data/../etc", []string{"/data"}) {
		t.Fatal("escaped unix path should be rejected")
	}
}

func TestBuildBrowseRootsDedupAndContext(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rt := filepath.Join(dir, "runtime")
	if err := os.MkdirAll(rt, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(dir, "extra-root")
	if err := os.MkdirAll(extra, 0o755); err != nil {
		t.Fatal(err)
	}
	contextDir := filepath.Join(dir, "context")
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	roots := BuildBrowseRoots(dir, rt, []string{dir, dir}, []string{extra}, []string{file, contextDir, outside, "relative-bad"})
	if len(roots) < 3 {
		t.Fatalf("expected roots with extra, got %+v", roots)
	}
	hasExtra := false
	for _, r := range roots {
		if r.Kind == "extra" {
			hasExtra = true
		}
	}
	if !hasExtra {
		t.Fatalf("missing extra root: %+v", roots)
	}
	for _, r := range roots {
		if pathKey(r.Path) == pathKey(outside) {
			t.Fatalf("untrusted context must not expand roots: %+v", roots)
		}
	}
	// data 与 global 去重后只保留一次 dir
	countDir := 0
	for _, r := range roots {
		if pathKey(r.Path) == pathKey(filepath.Clean(dir)) {
			countDir++
		}
	}
	if countDir != 1 {
		t.Fatalf("dir should appear once, got %d in %+v", countDir, roots)
	}
	// 上下文文件抬升为父目录，且与 data 去重
	for _, r := range roots {
		if r.Kind == "context" && pathKey(r.Path) == pathKey(filepath.Clean(dir)) {
			t.Fatal("context duplicate of data should be removed")
		}
	}
}

func TestServiceBrowseContextSymlinkCannotExpandAuthorization(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	svc := NewService(
		func() Policy { return Policy{GlobalFileRoots: []string{root}} },
		func() string { return root },
		func() string { return "" },
	)
	for _, browseRoot := range svc.BrowseRoots([]string{link}).Roots {
		if browseRoot.Kind == "context" && pathKey(browseRoot.Path) == pathKey(outside) {
			t.Fatalf("escaping symlink must not become context root: %+v", browseRoot)
		}
	}
	if _, err := svc.BrowseList(link, BrowseModeAny, 10, []string{link}); err == nil {
		t.Fatal("context root must not authorize escaping symlink")
	}
	if _, err := svc.BrowseStat(outside, []string{link}); err == nil {
		t.Fatal("context root must not authorize outside stat")
	}
}

func TestListBrowseDirFiltersAndLimit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sub := filepath.Join(root, "alpha")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "z-last.txt"), []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 填充足够条目触发 truncated
	for i := range 5 {
		if err := os.WriteFile(filepath.Join(root, "f"+string(rune('a'+i))+".txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 目录模式不返回文件
	res, err := ListBrowseDir(root, []string{root}, BrowseModeDirectory, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Name != "alpha" || res.Entries[0].Type != "dir" {
		t.Fatalf("dir mode entries=%+v", res.Entries)
	}
	if res.Parent != "" {
		t.Fatalf("root parent should be empty, got %q", res.Parent)
	}

	// file 模式仍返回目录用于导航，同时返回文件。
	fileRes, err := ListBrowseDir(root, []string{root}, BrowseModeFile, 100)
	if err != nil {
		t.Fatal(err)
	}
	hasDir, hasFile := false, false
	for _, item := range fileRes.Entries {
		hasDir = hasDir || item.Type == "dir"
		hasFile = hasFile || item.Type == "file"
	}
	if !hasDir || !hasFile {
		t.Fatalf("file mode must include navigable dirs and files: %+v", fileRes.Entries)
	}

	// any 模式文件+目录
	res, err = ListBrowseDir(root, []string{root}, BrowseModeAny, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Fatal("expected truncated")
	}
	if len(res.Entries) > 3 {
		t.Fatalf("limit broken: %d", len(res.Entries))
	}
	// 目录优先
	if len(res.Entries) > 0 && res.Entries[0].Type != "dir" {
		// 若 limit 很小可能只截到文件，但有目录时应优先
		hasDir := false
		for _, e := range res.Entries {
			if e.Type == "dir" {
				hasDir = true
			}
		}
		if hasDir && res.Entries[0].Type != "dir" {
			t.Fatalf("dirs should sort first: %+v", res.Entries)
		}
	}

	// 越权
	if _, err := ListBrowseDir(filepath.Dir(root), []string{root}, BrowseModeDirectory, 10); err == nil {
		t.Fatal("outside root should fail")
	}
}

func TestListBrowseDirSortsBeforeLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "z-directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a-file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ListBrowseDir(root, []string{root}, BrowseModeAny, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Type != "dir" || res.Entries[0].Name != "z-directory" {
		t.Fatalf("entries=%+v, want directory after sort-before-limit", res.Entries)
	}
	if !res.Truncated {
		t.Fatal("expected truncation when two entries are limited to one")
	}
}

func TestListBrowseDirFiltersBeforeLimit(t *testing.T) {
	root := t.TempDir()
	for i := range 250 {
		name := filepath.Join(root, "file-"+strings.Repeat("0", 4-len(strconv.Itoa(i)))+strconv.Itoa(i))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "visible-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := ListBrowseDir(root, []string{root}, BrowseModeDirectory, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Name != "visible-dir" {
		t.Fatalf("directory after nonmatching files must remain reachable: %+v", res)
	}
}

func TestListBrowseDirMissingAndNotDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	file := filepath.Join(root, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ListBrowseDir(filepath.Join(root, "nope"), []string{root}, BrowseModeDirectory, 10); err == nil {
		t.Fatal("missing should fail")
	}
	if _, err := ListBrowseDir(file, []string{root}, BrowseModeDirectory, 10); err == nil {
		t.Fatal("file should fail as dir list")
	}
}

func TestStatBrowsePath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	file := filepath.Join(root, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := StatBrowsePath(file, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Exists || st.Type != "file" || !st.Allowed {
		t.Fatalf("%+v", st)
	}
	missing := filepath.Join(root, "future-dir")
	st, err = StatBrowsePath(missing, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if st.Exists || !st.Allowed || st.Type != "missing" {
		t.Fatalf("missing allowed path: %+v", st)
	}
	outside := filepath.Join(filepath.Dir(root), "outside")
	if _, err = StatBrowsePath(outside, []string{root}); err == nil {
		t.Fatal("outside path must be rejected without probing")
	}
}

func TestServiceBrowseWrappers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rt := filepath.Join(root, "runtime")
	if err := os.MkdirAll(rt, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(
		func() Policy {
			return Policy{GlobalFileRoots: []string{root}}
		},
		func() string { return root },
		func() string { return rt },
	)
	roots := svc.BrowseRoots(nil)
	if len(roots.Roots) == 0 {
		t.Fatal("expected roots")
	}
	if roots.Platform == "" || roots.PathSeparator == "" {
		t.Fatalf("%+v", roots)
	}
	list, err := svc.BrowseList(root, BrowseModeDirectory, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if list.Path == "" {
		t.Fatal("empty path")
	}
	st, err := svc.BrowseStat(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Exists || st.Type != "dir" {
		t.Fatalf("%+v", st)
	}
}

func TestBrowseParentDoesNotEscape(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := ListBrowseDir(sub, []string{root}, BrowseModeDirectory, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Parent == "" || !PathUnderAnyRoot(res.Parent, []string{root}) {
		t.Fatalf("parent=%q", res.Parent)
	}
	// root 层 parent 为空
	res, err = ListBrowseDir(root, []string{root}, BrowseModeDirectory, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Parent != "" {
		t.Fatalf("root parent should be empty, got %q", res.Parent)
	}
}

func TestClampAndMode(t *testing.T) {
	t.Parallel()
	if ClampBrowseLimit(0) != browseDefaultLimit {
		t.Fatal("default limit")
	}
	if ClampBrowseLimit(9999) != browseMaxLimit {
		t.Fatal("max limit")
	}
	if NormalizeBrowseMode("") != BrowseModeDirectory {
		t.Fatal("default mode")
	}
	if NormalizeBrowseMode("FILE") != BrowseModeFile {
		t.Fatal("file mode")
	}
}

func TestWindowsPathKeyCase(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows only")
	}
	// pathKey 小写用于去重
	if pathKey(`C:\Data`) != pathKey(`c:\data`) {
		t.Fatal("windows path key should be case-insensitive")
	}
	// 盘符绝对路径
	p, err := NormalizeBrowsePath(`C:\Temp`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, ":") {
		t.Fatalf("unexpected %q", p)
	}
}
