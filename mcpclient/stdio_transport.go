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
type boundedCommandTransport struct {
	command  *exec.Cmd
	lifetime context.Context
}

func (t *boundedCommandTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.lifetime != nil && t.lifetime.Err() != nil {
		return nil, t.lifetime.Err()
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
	owner := &commandPipeOwner{command: t.command, stdout: stdout, stdin: stdin, done: make(chan struct{})}
	if t.lifetime != nil {
		go func() {
			select {
			case <-t.lifetime.Done():
				_ = owner.Close()
			case <-owner.done:
			}
		}()
	}
	return (&mcp.IOTransport{Reader: ownedCommandReader{owner}, Writer: ownedCommandWriter{owner}, MaxLineLength: maxResultBytes}).Connect(ctx)
}

type commandPipeOwner struct {
	command *exec.Cmd
	stdout  io.ReadCloser
	stdin   io.WriteCloser
	once    sync.Once
	err     error
	done    chan struct{}
}

func (p *commandPipeOwner) Close() error {
	p.once.Do(func() {
		defer close(p.done)
		// Windows pipe Close can itself wait for a pending ReadFile/WriteFile.
		// Arm termination independently before touching either pipe, or a silent
		// server can prevent us from ever reaching the grace timer.
		exited, timerDone := make(chan struct{}), make(chan struct{})
		go func() {
			timer := time.NewTimer(time.Second)
			defer timer.Stop()
			defer close(timerDone)
			select {
			case <-exited:
			case <-timer.C:
				_ = p.command.Process.Kill()
			}
		}()
		_ = p.stdin.Close()
		_ = p.stdout.Close()
		p.err = p.command.Wait()
		close(exited)
		<-timerDone
	})
	return p.err
}

type ownedCommandReader struct{ *commandPipeOwner }

func (p ownedCommandReader) Read(b []byte) (int, error) { return p.stdout.Read(b) }

type ownedCommandWriter struct{ *commandPipeOwner }

func (p ownedCommandWriter) Write(b []byte) (int, error) { return p.stdin.Write(b) }
