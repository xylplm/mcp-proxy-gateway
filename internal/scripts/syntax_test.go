package scripts

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRunner 实现 CommandRunner，按预设返回 stdout/stderr/err。
type fakeRunner struct {
	stdout  string
	stderr  string
	err     error
	missing bool // 是否把 err 报告为「运行时未安装」
	gotBase string
	gotArgs []string
}

func (f *fakeRunner) RunCommand(_ context.Context, base string, args []string, _ string) (string, string, error) {
	f.gotBase = base
	f.gotArgs = args
	return f.stdout, f.stderr, f.err
}

func (f *fakeRunner) IsRuntimeMissing(_ error) bool { return f.missing }

func TestValidateSyntaxNilRunnerSkips(t *testing.T) {
	// runner 为 nil 时不检测，直接通过。
	if err := ValidateSyntax("invalid !!!", "node", nil); err != nil {
		t.Fatalf("nil runner 应跳过，got %v", err)
	}
}

func TestValidateSyntaxRuntimeMissingSkips(t *testing.T) {
	// 解释器未装（runner 报告 IsRuntimeMissing=true）→ 跳过。
	r := &fakeRunner{err: errors.New("boom"), missing: true}
	if err := ValidateSyntax("x", "node", r); err != nil {
		t.Fatalf("运行时未装应跳过，got %v", err)
	}
}

func TestValidateSyntaxUnknownRuntimeSkips(t *testing.T) {
	r := &fakeRunner{}
	if err := ValidateSyntax("x", "ruby", r); err != nil {
		t.Fatalf("未知 runtime 应跳过，got %v", err)
	}
}

func TestValidateSyntaxJSErrorReturnsMessage(t *testing.T) {
	stderr := "/tmp/x.js:3\nSyntaxError: Unexpected token\n    at ...\n"
	r := &fakeRunner{stderr: stderr, err: errors.New("exit status 1")}
	err := ValidateSyntax("broken js", "node", r)
	if err == nil {
		t.Fatal("期望语法错误")
	}
	if !strings.Contains(err.Error(), "第 3 行") {
		t.Fatalf("缺行号：%v", err)
	}
	if !strings.Contains(err.Error(), "SyntaxError") {
		t.Fatalf("缺错误类型：%v", err)
	}
	// 确认调用 node --check
	if r.gotBase != "node" {
		t.Fatalf("base=%q", r.gotBase)
	}
	if len(r.gotArgs) < 2 || r.gotArgs[0] != "--check" {
		t.Fatalf("args=%v", r.gotArgs)
	}
}

func TestValidateSyntaxPythonErrorReturnsMessage(t *testing.T) {
	stderr := `  File "/tmp/x.py", line 4
    print("hi"
              ^
SyntaxError: unexpected EOF while parsing
`
	r := &fakeRunner{stderr: stderr, err: errors.New("exit status 1")}
	err := ValidateSyntax("broken py", "python3", r)
	if err == nil {
		t.Fatal("期望语法错误")
	}
	if !strings.Contains(err.Error(), "第 4 行") {
		t.Fatalf("缺行号：%v", err)
	}
	if !strings.Contains(err.Error(), "SyntaxError") {
		t.Fatalf("缺错误类型：%v", err)
	}
	if r.gotBase != "python3" {
		t.Fatalf("base=%q", r.gotBase)
	}
	// python -m py_compile <file>
	if len(r.gotArgs) < 3 || r.gotArgs[0] != "-m" || r.gotArgs[1] != "py_compile" {
		t.Fatalf("args=%v", r.gotArgs)
	}
}

func TestValidateSyntaxValidContentPasses(t *testing.T) {
	// 执行成功（err=nil）→ 通过。
	r := &fakeRunner{}
	if err := ValidateSyntax("print('hi')", "python3", r); err != nil {
		t.Fatalf("合法内容应通过：%v", err)
	}
}

func TestValidateSyntaxPythonRuntimeAliases(t *testing.T) {
	// runtime=python 应解析为 base=python。
	r := &fakeRunner{}
	_ = ValidateSyntax("x", "python", r)
	if r.gotBase != "python" {
		t.Fatalf("python 别名应解析为 python，got base=%q", r.gotBase)
	}
}
