package mcpclient

import (
	"context"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CommandTransport does not expose MaxLineLength. Own the command pipes and
// process, using the SDK's bounded decoder rather than its 16 MiB default.
type boundedCommandTransport struct{ command *exec.Cmd }

func (t *boundedCommandTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stdout, err := t.command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := t.command.StdinPipe()
	if err != nil {
		_ = stdout.Close()
		return nil, err
	}
	if err := t.command.Start(); err != nil {
		_ = stdout.Close()
		_ = stdin.Close()
		return nil, err
	}
	owner := &commandPipeOwner{command: t.command, stdout: stdout, stdin: stdin}
	return (&mcp.IOTransport{Reader: ownedCommandReader{owner}, Writer: ownedCommandWriter{owner}, MaxLineLength: maxResultBytes}).Connect(ctx)
}

type commandPipeOwner struct {
	command *exec.Cmd
	stdout  io.ReadCloser
	stdin   io.WriteCloser
	once    sync.Once
	err     error
}

func (p *commandPipeOwner) Close() error {
	p.once.Do(func() {
		// Close both pipes first to unblock SDK reads/writes, even if the server
		// ignores EOF. One owner alone reaps; no retry or process-name lookup.
		_ = p.stdin.Close()
		_ = p.stdout.Close()
		done := make(chan error, 1)
		go func() { done <- p.command.Wait() }()
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case p.err = <-done:
		case <-timer.C:
			_ = p.command.Process.Kill()
			p.err = <-done
		}
	})
	return p.err
}

type ownedCommandReader struct{ *commandPipeOwner }

func (p ownedCommandReader) Read(b []byte) (int, error) { return p.stdout.Read(b) }

type ownedCommandWriter struct{ *commandPipeOwner }

func (p ownedCommandWriter) Write(b []byte) (int, error) { return p.stdin.Write(b) }
