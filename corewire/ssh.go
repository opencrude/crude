package corewire

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// SSHOptions intentionally does not expose arbitrary -o options or remote paths.
// Host can be a configured SSH alias; user, port, and identity are explicit.
type SSHOptions struct {
	Host         string
	User         string
	Port         int
	IdentityFile string
}

var sshHost = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
var sshUser = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

func sshArgs(o SSHOptions) ([]string, error) {
	if !sshHost.MatchString(o.Host) || (o.User != "" && !sshUser.MatchString(o.User)) || o.Port < 0 || o.Port > 65535 {
		return nil, errors.New("invalid SSH host, user or port")
	}
	if strings.ContainsAny(o.IdentityFile, "\x00\r\n") {
		return nil, errors.New("invalid SSH identity file")
	}
	args := []string{"-T", "-a", "-x", "-o", "StrictHostKeyChecking=yes", "-o", "ClearAllForwardings=yes", "-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-o", "PermitLocalCommand=no", "-o", "RemoteCommand=none", "-o", "RequestTTY=no", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "BatchMode=yes"}
	if o.User != "" {
		args = append(args, "-l", o.User)
	}
	if o.Port != 0 {
		args = append(args, "-p", strconv.Itoa(o.Port))
	}
	if o.IdentityFile != "" {
		args = append(args, "-i", o.IdentityFile)
	}
	return append(args, "--", o.Host, "crude --core-stdio"), nil
}

// DialSSH starts system ssh, not a shell. Only the fixed remote command reaches
// the remote shell; workspace paths and operations belong in framed messages.
// Close/cancellation kills and joins the owned subprocess. Authentication is
// noninteractive, and normal system SSH config/known_hosts remain authoritative.
func DialSSH(ctx context.Context, o SSHOptions) (io.ReadWriteCloser, error) {
	return dialSSH(ctx, "ssh", o)
}

type sshConn struct {
	in         io.WriteCloser
	out        io.ReadCloser
	cmd        *exec.Cmd
	done       chan struct{}
	once       sync.Once
	diagnostic *sshDiagnostic
}

// Keep only a bounded tail. SSH/proxy output can be arbitrarily large; it must
// never become an unbounded retained buffer or inject terminal control bytes.
type sshDiagnostic struct {
	mu   sync.Mutex
	text []byte
}

func (d *sshDiagnostic) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	const cap = 4096
	if len(p) >= cap {
		d.text = append(d.text[:0], p[len(p)-cap:]...)
	} else {
		if len(d.text)+len(p) > cap {
			d.text = append(d.text[:0], d.text[len(d.text)+len(p)-cap:]...)
		}
		d.text = append(d.text, p...)
	}
	return len(p), nil
}
func (d *sshDiagnostic) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, string(d.text)))
}

func dialSSH(ctx context.Context, program string, o SSHOptions) (io.ReadWriteCloser, error) {
	args, err := sshArgs(o)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(program, args...)
	// A configured proxy child must not keep Wait blocked through inherited stderr.
	cmd.WaitDelay = time.Second
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		return nil, err
	}
	diagnostic := &sshDiagnostic{}
	cmd.Stderr = diagnostic
	if err = cmd.Start(); err != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, err
	}
	c := &sshConn{in: in, out: out, cmd: cmd, done: make(chan struct{}), diagnostic: diagnostic}
	go func() { _ = cmd.Wait(); close(c.done) }()
	go func() {
		select {
		case <-ctx.Done():
			_ = c.Close()
		case <-c.done:
		}
	}()
	return c, nil
}

func (c *sshConn) failure(err error) error {
	if detail := c.diagnostic.String(); detail != "" {
		return fmt.Errorf("SSH transport failed: %s: %w", detail, err)
	}
	return err
}
func (c *sshConn) Read(b []byte) (int, error) {
	n, err := c.out.Read(b)
	if err != nil {
		_ = c.Close() // EOF is terminal for framed core transport; join the owner.
		return n, c.failure(err)
	}
	return n, nil
}
func (c *sshConn) Write(b []byte) (int, error) {
	n, err := c.in.Write(b)
	if err != nil {
		return n, c.failure(err)
	}
	return n, nil
}
func (c *sshConn) Close() error {
	c.once.Do(func() { _ = c.in.Close(); _ = c.out.Close(); _ = c.cmd.Process.Kill() })
	<-c.done
	// Closing the shell intentionally kills its relay, not the retained core.
	return nil
}
