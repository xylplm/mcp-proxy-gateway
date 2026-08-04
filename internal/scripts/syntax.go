package scripts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// CommandRunner 在 runtime 环境下执行一条命令，返回 stdout/stderr。
// 由上层注入（runtime.Service 实现该接口），保持 scripts 包不直接依赖 runtime。
type CommandRunner interface {
	RunCommand(ctx context.Context, base string, args []string, cwd string) (stdout, stderr string, err error)
	// IsRuntimeMissing 判断 RunCommand 返回的错误是否为「解释器未安装」。
	// scripts 据此跳过语法检测而非当作硬失败（不阻断保存）。
	IsRuntimeMissing(err error) bool
}

// syntaxCheckTimeout 单次语法检测超时（避免异常脚本挂起保存流程）。
const syntaxCheckTimeout = 15 * time.Second

// ValidateSyntax 对脚本内容做语法检测（JS: node --check；Python: python -m py_compile）。
//
// 行为：
//   - runner 为 nil（未注入运行时服务）→ 跳过，返回 nil。
//   - 解释器未安装（runner 返回 ErrRuntimeMissing，errors.Is 命中）→ 跳过，返回 nil。
//   - 检测超时 → 跳过，返回 nil（不阻断保存）。
//   - 语法错误 → 返回带首个错误位置（行号）的中文错误。
//   - 语法正确 → nil。
//
// content 写入临时文件，执行后清理。
func ValidateSyntax(content, runtime string, runner CommandRunner) error {
	if runner == nil {
		return nil
	}
	base := normalizeSyntaxRuntime(runtime)
	if base == "" {
		return nil // 未知运行时，跳过
	}
	args := syntaxCheckArgs(base)
	if args == nil {
		return nil
	}

	// 写临时文件（带语言后缀，便于解释器识别 / 错误行号对齐）。
	ext := ".txt"
	if base == "node" {
		ext = ".js"
	} else {
		ext = ".py"
	}
	tmp, err := os.CreateTemp("", "mpg-syntax-*"+ext)
	if err != nil {
		return nil // 无法建临时文件时跳过，不阻断保存
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.RemoveAll(tmpPath) }()
	if _, werr := tmp.WriteString(content); werr != nil {
		_ = tmp.Close()
		return nil
	}
	_ = tmp.Close()

	ctx, cancel := context.WithTimeout(context.Background(), syntaxCheckTimeout)
	defer cancel()
	fullArgs := append(append([]string{}, args...), tmpPath)
	_, stderr, runErr := runner.RunCommand(ctx, base, fullArgs, filepath.Dir(tmpPath))
	if runErr != nil {
		// 解释器未装 → 跳过（类型化判断，避免脆弱的字符串匹配）。
		if runner.IsRuntimeMissing(runErr) {
			return nil
		}
		// 超时 → 跳过（避免异常脚本挂起保存流程）。
		if errors.Is(runErr, context.DeadlineExceeded) {
			return nil
		}
		// 其余执行错误（含语法错误）：尝试从 stderr 提取首个错误行。
		if msg := extractSyntaxError(stderr, base); msg != "" {
			return fmt.Errorf("语法错误：%s", msg)
		}
		// 无法解析错误行时，给出执行失败的通用提示（保留 stderr 尾部）。
		tail := strings.TrimSpace(stderr)
		if len(tail) > 300 {
			tail = tail[len(tail)-300:]
		}
		if tail != "" {
			return fmt.Errorf("语法检测失败：%s", tail)
		}
		return fmt.Errorf("语法检测失败：%v", runErr)
	}
	return nil
}

func normalizeSyntaxRuntime(runtime string) string {
	base := strings.ToLower(strings.TrimSpace(runtime))
	switch base {
	case "node":
		return "node"
	case "python", "python3":
		return base
	default:
		return ""
	}
}

func syntaxCheckArgs(base string) []string {
	switch base {
	case "node":
		// node --check 仅做语法解析，不执行。
		return []string{"--check"}
	case "python", "python3":
		// python -m py_compile 编译为字节码，检测语法错误。
		return []string{"-m", "py_compile"}
	default:
		return nil
	}
}

var (
	// JS: 形如 "file.js:3" / "SyntaxError: Unexpected token" / "ReferenceError"
	jsSyntaxLine = regexp.MustCompile(`(?m)^(?:\S+\.js):(?:line\s+)?(\d+)`)
	jsSyntaxMsg  = regexp.MustCompile(`(?m)^(SyntaxError|ReferenceError|TypeError)[^\n]*`)
	// Python: 形如 "  File \"x.py\", line 3" + 下一行 "    print('hi)"
	//          以及 "SyntaxError: invalid syntax"
	pySyntaxLine = regexp.MustCompile(`(?m)File "[^"]+", line (\d+)`)
	pySyntaxMsg  = regexp.MustCompile(`(?m)^(SyntaxError|IndentationError|TabError)[^\n]*`)
)

func extractSyntaxError(stderr, base string) string {
	var lineRe, msgRe *regexp.Regexp
	if base == "node" {
		lineRe, msgRe = jsSyntaxLine, jsSyntaxMsg
	} else {
		lineRe, msgRe = pySyntaxLine, pySyntaxMsg
	}
	msg := ""
	if m := msgRe.FindString(stderr); m != "" {
		msg = strings.TrimSpace(m)
	}
	if lm := lineRe.FindStringSubmatch(stderr); len(lm) > 1 {
		if msg == "" {
			return fmt.Sprintf("第 %s 行", lm[1])
		}
		return fmt.Sprintf("第 %s 行：%s", lm[1], msg)
	}
	return msg
}
