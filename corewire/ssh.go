package corewire

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
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
	in   io.WriteCloser
	out  io.ReadCloser
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
	err  error
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
	// Discard stderr rather than retain an unbounded diagnostic buffer.
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, err
	}
	c := &sshConn{in: in, out: out, cmd: cmd, done: make(chan struct{})}
	go func() { c.err = cmd.Wait(); close(c.done) }()
	go func() {
		select {
		case <-ctx.Done():
			_ = c.Close()
		case <-c.done:
		}
	}()
	return c, nil
}

func (c *sshConn) Read(b []byte) (int, error)  { return c.out.Read(b) }
func (c *sshConn) Write(b []byte) (int, error) { return c.in.Write(b) }
func (c *sshConn) Close() error {
	c.once.Do(func() { _ = c.in.Close(); _ = c.out.Close(); _ = c.cmd.Process.Kill() })
	<-c.done
	return c.err
}
