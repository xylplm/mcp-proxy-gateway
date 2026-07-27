//go:build windows

package transport

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/runtime"
)

// windowsCommandTransport starts the child itself so the Job Object can be
// attached immediately after Start, before the MCP handshake begins.
type windowsCommandTransport struct {
	cmd *exec.Cmd
}

func newCommandTransport(cmd *exec.Cmd, hardening bool) mcp.Transport {
	if !hardening {
		return &mcp.CommandTransport{Command: cmd, TerminateDuration: time.Second}
	}
	return &windowsCommandTransport{cmd: cmd}
}

func (t *windowsCommandTransport) Connect(context.Context) (mcp.Connection, error) {
	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := t.cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := t.cmd.Start(); err != nil {
		return nil, err
	}
	if err := runtime.AttachProcessHardening(t.cmd); err != nil {
		_ = t.cmd.Process.Kill()
		_ = t.cmd.Wait()
		return nil, fmt.Errorf("Windows 进程加固失败：%w", err)
	}
	inner, err := (&mcp.IOTransport{Reader: stdout, Writer: stdin}).Connect(context.Background())
	if err != nil {
		runtime.TerminateProcessTree(t.cmd)
		_ = t.cmd.Wait()
		return nil, err
	}
	return &windowsCommandConnection{inner: inner, cmd: t.cmd}, nil
}

type windowsCommandConnection struct {
	inner mcp.Connection
	cmd   *exec.Cmd
	once  sync.Once
	err   error
}

func (c *windowsCommandConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	return c.inner.Read(ctx)
}

func (c *windowsCommandConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	return c.inner.Write(ctx, msg)
}

func (c *windowsCommandConnection) SessionID() string {
	return c.inner.SessionID()
}

func (c *windowsCommandConnection) Close() error {
	c.once.Do(func() {
		c.err = c.inner.Close()
		runtime.TerminateProcessTree(c.cmd)
	})
	return c.err
}
