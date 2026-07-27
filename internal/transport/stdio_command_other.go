//go:build !windows

package transport

import (
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newCommandTransport(cmd *exec.Cmd, _ bool) mcp.Transport {
	return &mcp.CommandTransport{Command: cmd, TerminateDuration: time.Second}
}
