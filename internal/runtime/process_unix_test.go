//go:build unix

package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTerminateProcessTreeKillsDescendantAfterLeaderReaped(t *testing.T) {
	pidFile := t.TempDir() + "/child.pid"
	script := fmt.Sprintf(`sh -c 'trap "" TERM; while :; do sleep 1; done' & echo $! > %q`, pidFile)
	cmd := exec.Command("sh", "-c", script)
	ApplySandbox(cmd, SandboxOptions{Enabled: true})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}

	var childPID int
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(pidFile)
		if err == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			if childPID > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID <= 0 {
		t.Fatal("descendant pid was not recorded")
	}
	if err := syscall.Kill(childPID, syscall.Signal(0)); err != nil {
		t.Fatalf("descendant should be alive before cleanup: %v", err)
	}

	TerminateProcessTree(cmd)
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPID, syscall.Signal(0)); err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived process-tree cleanup", childPID)
}
