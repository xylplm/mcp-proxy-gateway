package runtime

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// fastEchoCommand 返回一条「立刻输出一行并退出」的命令。
// 按平台选择解释器，但用例本身在所有平台都跑：丢输出的回归不该只有 Linux CI 能发现。
func fastEchoCommand(text string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/c", "echo " + text}
	}
	return "/bin/sh", []string{"-c", "echo " + text}
}

// 曾经的实现用 cmd.StdoutPipe() 且在读取完成前就调用 Wait，而 Wait 会关闭管道；
// echo 这类瞬间退出的命令因此整段丢失 stdout，在依赖管理上表现为
// 「解析 pip 输出失败：unexpected end of JSON input」。
//
// 重复多次以放大竞态可见性：单次运行可能侥幸通过。
func TestRunBoundedCapturesFastCommandOutput(t *testing.T) {
	const marker = "mpg-runbounded-probe"
	name, args := fastEchoCommand(marker)
	for i := 0; i < 30; i++ {
		cmd := exec.Command(name, args...)
		cmd.WaitDelay = depKillGrace
		stdout, stderr, err := runBounded(cmd, nil)
		if err != nil {
			t.Fatalf("第 %d 次执行失败：%v（stderr=%q）", i+1, err, stderr)
		}
		if !strings.Contains(stdout, marker) {
			t.Fatalf("第 %d 次丢失 stdout：%q", i+1, stdout)
		}
	}
}

// 多行输出必须按行完整落到缓冲，且末尾没有换行的残行也不能丢。
func TestRunBoundedKeepsAllLinesIncludingTrailingPartial(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("printf 无换行输出依赖 POSIX shell")
	}
	cmd := exec.Command("/bin/sh", "-c", `printf 'first\nsecond\nthird'`)
	cmd.WaitDelay = depKillGrace
	stdout, _, err := runBounded(cmd, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := stdout, "first\nsecond\nthird\n"; got != want {
		t.Fatalf("stdout=%q, want %q", got, want)
	}
}

// logFn 必须逐行收到 stdout 与 stderr，供依赖管理写实时日志。
func TestRunBoundedStreamsLinesToLogFn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("双流输出依赖 POSIX shell")
	}
	cmd := exec.Command("/bin/sh", "-c", `echo out-line; echo err-line 1>&2`)
	cmd.WaitDelay = depKillGrace
	var streams []string
	_, _, err := runBounded(cmd, func(stream, line string) {
		streams = append(streams, stream+":"+line)
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	joined := strings.Join(streams, ",")
	if !strings.Contains(joined, "stdout:out-line") || !strings.Contains(joined, "stderr:err-line") {
		t.Fatalf("logFn 未收到完整行：%v", streams)
	}
}
