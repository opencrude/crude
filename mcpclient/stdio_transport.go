package mcpclient

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CommandTransport does not expose MaxLineLength. Own the command pipes and
// process, using the SDK's bounded decoder rather than its 16 MiB default.
type boundedCommandTransport struct {
	command  *exec.Cmd
	lifetime context.Context
	// stderr retains the server's diagnostic tail. Optional for fixtures.
	stderr *stderrTail
}

// stderrTailBytes bounds retained diagnostics; servers may log continuously.
const stderrTailBytes = 4 << 10

// stderrTail keeps the last bytes a local server wrote to stderr. The write end
// is a real file handle, so exec.Cmd.Wait never waits on a copier goroutine
// that a surviving grandchild (npx → node) could hold open indefinitely; the
// reader simply ends at EOF once every holder has exited.
type stderrTail struct {
	mu   sync.Mutex
	data []byte
	done chan struct{}
}

func newStderrTail() *stderrTail { return &stderrTail{done: make(chan struct{})} }

func (t *stderrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.data = append(t.data, p...)
	if excess := len(t.data) - stderrTailBytes; excess > 0 {
		t.data = append(t.data[:0:0], t.data[excess:]...)
	}
	return len(p), nil
}

// Text returns the retained tail. After a process exit, the last diagnostic
// line can still be in the pipe; wait briefly for EOF so a crash reason such
// as "Could not open main display" is not lost to scheduling.
func (t *stderrTail) Text(settle time.Duration) string {
	if t == nil {
		return ""
	}
	if settle > 0 {
		timer := time.NewTimer(settle)
		select {
		case <-t.done:
		case <-timer.C:
		}
		timer.Stop()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(strings.ToValidUTF8(string(t.data), "\uFFFD"))
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
	var stderrRead, stderrWrite *os.File
	if t.stderr != nil {
		if stderrRead, stderrWrite, err = os.Pipe(); err != nil {
			_ = stdout.Close()
			_ = stdin.Close()
			return nil, err
		}
		t.command.Stderr = stderrWrite
	}
	if err := t.command.Start(); err != nil {
		_ = stdout.Close()
		_ = stdin.Close()
		if stderrWrite != nil {
			_ = stderrWrite.Close()
			_ = stderrRead.Close()
		}
		return nil, err
	}
	if stderrWrite != nil {
		// The child owns its copy; keeping ours would prevent EOF forever.
		_ = stderrWrite.Close()
		go func(tail *stderrTail) {
			defer close(tail.done)
			defer func() { _ = stderrRead.Close() }()
			_, _ = io.Copy(tail, stderrRead)
		}(t.stderr)
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
