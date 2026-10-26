package corewire

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
var sshBuild = regexp.MustCompile(`^[a-f0-9]{64}$`)
var sshDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var sshDataName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

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
	return startSSH(ctx, program, args)
}

func startSSH(ctx context.Context, program string, args []string) (io.ReadWriteCloser, error) {
	var err error
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
	// The connection owns the read end, not exec.Cmd.Wait: a fast SSH exit must
	// leave all buffered stdout readable through EOF. Use an OS pipe directly
	// so Wait neither closes the reader nor waits for a copy to a slow peer.
	out, childOut, err := os.Pipe()
	if err != nil {
		_ = in.Close()
		return nil, err
	}
	cmd.Stdout = childOut
	diagnostic := &sshDiagnostic{}
	cmd.Stderr = diagnostic
	err = cmd.Start()
	// Start has either inherited its own writer or failed; retaining the
	// parent's copy would hide EOF after the owned process exits.
	_ = childOut.Close()
	if err != nil {
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

// DialSSHBuild selects an immutable native image by its exact source cohort,
// independently of its image digest. A PATH image is only a candidate: the caller
// must enforce the exact protocol handshake. This never installs, replaces PATH
// entries, or interpolates a project path. System SSH trust rules still apply.
func DialSSHBuild(ctx context.Context, o SSHOptions, namespace, cohort string) (io.ReadWriteCloser, error) {
	if !sshDataName.MatchString(namespace) || !sshBuild.MatchString(cohort) {
		return nil, errors.New("invalid SSH build identity")
	}
	args, err := sshHelperArgs(o, sshRunBuild, namespace, cohort)
	if err != nil {
		return nil, err
	}
	return startSSH(ctx, "ssh", args)
}

// SSHHost describes facts observed through the verified SSH channel, not paths
// supplied by a workspace. InstallSSHImage rechecks all of them before writing.
type SSHHost struct {
	OS, Arch, DataDir string
}

func ProbeSSHHost(ctx context.Context, o SSHOptions, namespace string) (SSHHost, error) {
	if !sshDataName.MatchString(namespace) {
		return SSHHost{}, errors.New("invalid SSH data namespace")
	}
	out, err := runSSHHelper(ctx, o, sshProbeHost, nil, namespace)
	if err != nil {
		return SSHHost{}, err
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[2], "/") || strings.ContainsFunc(strings.Join(lines, ""), unicode.IsControl) {
		return SSHHost{}, errors.New("invalid SSH host probe")
	}
	return SSHHost{OS: lines[0], Arch: lines[1], DataDir: lines[2]}, nil
}

// InstallSSHImage publishes one closed, digest-verified candidate by no-replace
// hard link. No running executable or existing data is overwritten. Unsupported
// hosts and filesystems fail explicitly. Cancellation joins the SSH command and
// its transfer before returning. The caller owns explicit consent for the source
// cohort and the independent, full SHA-256 of the native image.
func InstallSSHImage(ctx context.Context, o SSHOptions, namespace, cohort, digest string, host SSHHost, image *os.File) error {
	if !sshDataName.MatchString(namespace) || !sshBuild.MatchString(cohort) || !sshDigest.MatchString(digest) ||
		(host.OS != "Linux" && host.OS != "Darwin") || (host.Arch != "x86_64" && host.Arch != "aarch64" && host.Arch != "arm64") ||
		!strings.HasPrefix(host.DataDir, "/") || strings.ContainsFunc(host.DataDir, unicode.IsControl) || image == nil {
		return errors.New("invalid SSH install identity")
	}
	input := io.MultiReader(strings.NewReader(host.DataDir+"\n"), image)
	_, err := runSSHHelper(ctx, o, sshInstallImage, input, namespace, cohort, digest, host.OS, host.Arch)
	return err
}

// Only constant scripts and closed, validated scalar operands call this helper.
// The remote shell sees one quoted script, never a user-selected command.
func sshHelperArgs(o SSHOptions, script string, operands ...string) ([]string, error) {
	args, err := sshArgs(o)
	if err != nil {
		return nil, err
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	command := "sh -c " + quote(script) + " sh"
	for _, operand := range operands {
		command += " " + quote(operand)
	}
	args[len(args)-1] = command
	return args, nil
}

type sshHelperOutput struct{ bytes.Buffer }

func (b *sshHelperOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 16<<10 {
		return 0, errors.New("SSH helper output exceeds limit")
	}
	return b.Buffer.Write(p)
}

func runSSHHelper(ctx context.Context, o SSHOptions, script string, input io.Reader, operands ...string) ([]byte, error) {
	args, err := sshHelperArgs(o, script, operands...)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.WaitDelay = time.Second
	out, diagnostic := &sshHelperOutput{}, &sshDiagnostic{}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, out, diagnostic
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("SSH helper failed: %s: %w", diagnostic.String(), errors.Join(ctx.Err(), err))
	}
	return out.Bytes(), nil
}

// CRUDE_HOME is read on the remote only; local profile/workspace paths never
// select a remote destination. A relative override is refused rather than guessed.
const sshHostRoot = `set -eu
root=${CRUDE_HOME:-${HOME:?}}
case "$root" in /*) ;; *) echo 'Crude home must be absolute' >&2; exit 1;; esac
root="$root/.$1"
`

const sshProbeHost = sshHostRoot + `printf '%s\n' "$(uname -s)" "$(uname -m)" "$root"
`

const sshRunBuild = sshHostRoot + `image="$root/remote-bin/crude-$2"
if [ -f "$image" ] && [ ! -L "$image" ] && [ -x "$image" ]; then exec "$image" --core-stdio; fi
if command -v crude >/dev/null 2>&1; then exec crude --core-stdio; fi
echo 'CRUDE_REMOTE_CORE_MISSING: matching Crude core installation required' >&2
exit 127
`

const sshInstallImage = sshHostRoot + `IFS= read -r expected
[ "$root" = "$expected" ] || { echo 'Remote data directory changed; review again' >&2; exit 1; }
[ "$(uname -s)" = "$4" ] && [ "$(uname -m)" = "$5" ] || { echo 'Remote platform changed; review again' >&2; exit 1; }
umask 077
[ ! -L "$root" ] && [ ! -L "$root/remote-bin" ] || { echo 'Refusing symlink install directory' >&2; exit 1; }
mkdir -p "$root/remote-bin"
dir="$root/remote-bin"
[ -O "$root" ] && [ -O "$dir" ] || { echo 'Install directory is not owned by this user' >&2; exit 1; }
chmod 700 "$dir"
tmp=$(mktemp "$dir/.install-XXXXXXXX")
trap 'rm -f "$tmp"' EXIT HUP INT TERM
cat > "$tmp"
digest() {
 if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d ' ' -f 1
 elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d ' ' -f 1
 else echo 'No SHA-256 verifier on remote host' >&2; return 1; fi
}
[ "$(digest "$tmp")" = "$3" ] || { echo 'Image digest mismatch; nothing installed' >&2; exit 1; }
chmod 700 "$tmp"
target="$dir/crude-$2"
[ ! -d "$target" ] && [ ! -L "$target" ] || { echo 'Immutable image conflict; existing image retained' >&2; exit 1; }
if ! ln "$tmp" "$target" 2>/dev/null; then
 [ -f "$target" ] && [ ! -L "$target" ] && [ -x "$target" ] && [ "$(digest "$target")" = "$3" ] || { echo 'Immutable image conflict; existing image retained' >&2; exit 1; }
fi
printf 'installed\n'
`

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
		_ = c.Close() // Join stderr capture before reporting a terminal pipe error.
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
