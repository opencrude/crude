package app

// Distinct-host acceptance stays test-only. Remote selection is an explicit SSH
// user/home, never a production override, host-key bypass or hidden root profile.

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"crude/buildinfo"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/sessiondb"
	"crude/shellport/remote"
	"crude/types/control"
	"crude/types/inspect"
)

type liveDiagnosticRemote struct {
	options             corewire.SSHOptions
	home, image, helper string
}

// An independent observer can diagnose a stalled acceptance harness through the
// same offered commands, without restarting it or reading provider credentials.
func TestLiveDiagnosticsRemoteInspectExisting(t *testing.T) {
	if os.Getenv("CRUDE_LIVE_DIAGNOSTICS") != "1" || os.Getenv("CRUDE_DIAGNOSTICS_REMOTE") == "" {
		t.Skip("explicit existing remote-core observation")
	}
	r, err := liveDiagnosticRemoteConfig()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	c, err := liveDiagnosticRemoteDialer("")(ctx, "diagnostics-harness-observer")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	var attached coreprotocol.AttachResult
	if err := c.Call(ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: filepath.Join(r.home, "project")}, &attached); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.ControlClientRegister, &coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read}}, nil); err != nil {
		t.Fatal(err)
	}
	for i, item := range []struct{ command, view, filter string }{
		{"diagnostics.core", "workspaces", ""}, {"diagnostics.core", "clients", ""},
		{"diagnostics.workspace", "agents", ""}, {"performance.core", "series", "agent"},
	} {
		id := fmt.Sprintf("harness-diagnosis-%d", i)
		input, _ := json.Marshal(coreprotocol.DiagnosticsInspectRequest{Page: inspect.Request{View: item.view, Filter: item.filter, Limit: 8, MaxBytes: 16 << 10}})
		var result control.Result
		if _, err := c.CallWith(ctx, attached.WorkspaceID, coreprotocol.ControlExecute, &control.Request{ID: id, CommandID: item.command, Target: control.Target{WorkspaceID: attached.WorkspaceID}, Input: input}, &result, coreclient.CallOptions{ID: id}); err != nil {
			t.Fatal(err)
		}
		t.Logf("ON_DEMAND_HARNESS_DIAGNOSIS command=%s view=%s output=%s", item.command, item.view, result.Output)
	}
}

func liveDiagnosticRemoteConfig() (liveDiagnosticRemote, error) {
	r := liveDiagnosticRemote{options: corewire.SSHOptions{Host: os.Getenv("CRUDE_DIAGNOSTICS_REMOTE"), User: os.Getenv("CRUDE_DIAGNOSTICS_REMOTE_USER"), IdentityFile: os.Getenv("CRUDE_DIAGNOSTICS_REMOTE_IDENTITY")}, home: os.Getenv("CRUDE_DIAGNOSTICS_REMOTE_HOME"), image: os.Getenv("CRUDE_DIAGNOSTICS_REMOTE_IMAGE"), helper: os.Getenv("CRUDE_DIAGNOSTICS_REMOTE_TEST_IMAGE")}
	if os.Getenv("CRUDE_LIVE_DIAGNOSTICS") != "1" || r.options.User == "" || r.options.User == "root" || !filepath.IsAbs(r.options.IdentityFile) || !filepath.IsAbs(r.home) || !strings.HasPrefix(filepath.Base(r.home), "crdiag") || !strings.HasPrefix(r.image, r.home+"/") || !strings.HasPrefix(r.helper, r.home+"/") {
		return r, fmt.Errorf("remote diagnostics requires explicit disposable non-root SSH identity, crdiag home, and private image paths")
	}
	return r, nil
}

func liveDiagnosticRemoteDialer(home string) remote.Dialer {
	r, err := liveDiagnosticRemoteConfig()
	if err != nil {
		return func(context.Context, string) (*coreclient.Client, error) { return nil, err }
	}
	return remoteSSHDialer(home, r.options)
}

func diagnosticQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func (r liveDiagnosticRemote) command(ctx context.Context, script string, operands ...string) *exec.Cmd {
	args := []string{"-T", "-a", "-x", "-o", "StrictHostKeyChecking=yes", "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-o", "ClearAllForwardings=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ConnectTimeout=10", "-i", r.options.IdentityFile, "-l", r.options.User, "--", r.options.Host}
	command := "sh -c " + diagnosticQuote(script) + " sh"
	for _, arg := range operands {
		command += " " + diagnosticQuote(arg)
	}
	return exec.CommandContext(ctx, "ssh", append(args, command)...)
}

func startLiveDiagnosticRemoteCore(t *testing.T, ctx context.Context, live bool) *liveDiagnosticCore {
	t.Helper()
	r, err := liveDiagnosticRemoteConfig()
	if err != nil {
		t.Fatal(err)
	}
	probe, err := corewire.ProbeSSHHost(ctx, r.options, buildinfo.DataDirName)
	if err != nil || probe.DataDir != DataDir(r.home) {
		t.Fatal("remote SSH profile does not match explicit disposable home", err)
	}
	// The parent provisions only these private files. No arbitrary config or
	// installation is exported by a product command or selected by a model.
	const start = `set -eu
test "$(cat "$1/.diagnostics-test-owned")" = crude-diagnostics-2026-10-26
test ! -S "$1/.crude/corewire/core.sock"
test -x "$2" && test -x "$3" && test -r "$1/input.json"
env CRUDE_APP_TEST_MAIN=0 CRUDE_DIAGNOSTICS_HELPER=1 CRUDE_DIAGNOSTICS_HELPER_LIVE="$4" CRUDE_DIAGNOSTICS_HELPER_REMOTE=1 CRUDE_DIAGNOSTICS_HOME="$1" CRUDE_DIAGNOSTICS_CONFIG="$1/input.json" CRUDE_DIAGNOSTICS_REMOTE_QUIC="$5" CRUDE_DIAGNOSTICS_REMOTE_TLS="$6" nohup "$3" -test.run='^TestLiveDiagnosticsCoreHelper$' -test.timeout=25m >"$1/helper.log" 2>&1 < /dev/null &
pid=$!
python3 -c 'import os,sys,time; p=int(sys.argv[1]); sock=sys.argv[2]; deadline=time.monotonic()+30
while time.monotonic()<deadline:
 if os.path.exists(sock): print(p); break
 if not os.path.exists("/proc/"+str(p)): raise SystemExit("owned helper exited before socket readiness")
 time.sleep(.05)
else: raise SystemExit("owned helper socket timeout")' "$pid" "$1/.crude/corewire/core.sock"
`
	cmd := r.command(ctx, start, r.home, r.image, r.helper, strconv.Itoa(boolIntDiagnostic(live)), os.Getenv("CRUDE_DIAGNOSTICS_REMOTE_QUIC"), os.Getenv("CRUDE_DIAGNOSTICS_REMOTE_TLS"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("start disposable remote helper: %v: %.2048s", err, out)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 1 {
		t.Fatal("invalid owned remote helper pid", err)
	}
	server := &liveDiagnosticCore{home: r.home, pid: pid, remote: &r}
	t.Cleanup(func() { server.stop(t) })
	client, err := diagnosticDialer("")(ctx, "diagnostics-remote-readiness")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	var status coreprotocol.CoreStatus
	if err := client.Call(ctx, "", coreprotocol.CoreStatusMethod, &coreprotocol.Empty{}, &status); err != nil || !status.Server || status.PID != pid {
		t.Fatalf("remote owner mismatch: pid=%d server=%v err=%v", status.PID, status.Server, err)
	}
	carrier, _ := client.Carrier()
	t.Logf("REMOTE_DIAGNOSTICS_CORE pid=%d carrier=%s distinct_host=%s", pid, carrier, r.options.Host)
	return server
}

func (r liveDiagnosticRemote) stop(t *testing.T, pid int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const stop = `set -eu
test "$(cat "$1/.diagnostics-test-owned")" = crude-diagnostics-2026-10-26
if [ -e "/proc/$3/exe" ]; then
 test "$(readlink "/proc/$3/exe")" = "$4"
 env CRUDE_HOME="$1" "$2" --core-stop --force
fi
python3 -c 'import os,sys,time; path="/proc/"+sys.argv[1]+"/exe"; deadline=time.monotonic()+20
while os.path.exists(path) and time.monotonic()<deadline: time.sleep(.05)
if os.path.exists(path): raise SystemExit("owned remote helper did not stop")' "$3"
`
	out, err := r.command(ctx, stop, r.home, r.image, strconv.Itoa(pid), r.helper).CombinedOutput()
	if err != nil {
		t.Errorf("remote helper stop did not settle pid=%d: %v: %.2048s", pid, err, out)
	}
}

func liveDiagnosticRemoteMessages(t *testing.T, project, id string) []sessiondb.MessageWithMeta {
	t.Helper()
	r, err := liveDiagnosticRemoteConfig()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	// SQLite backup observes an actual accepted journal without exporting a new
	// product API or copying an inconsistent live main file without its WAL.
	const snapshot = `set -eu
test "$(cat "$1/.diagnostics-test-owned")" = crude-diagnostics-2026-10-26
python3 -c 'import gzip,hashlib,os,sqlite3,sys; home,project,session=sys.argv[1:]; assert "/" not in session and "\\" not in session
ws=hashlib.sha256(os.path.abspath(project).encode()).hexdigest()[:16]
p=os.path.join(home,".crude","workspaces",ws,"sessions",session+".db")
src=sqlite3.connect("file:"+p+"?mode=ro",uri=True); dst=sqlite3.connect(":memory:"); src.backup(dst); sys.stdout.buffer.write(gzip.compress(dst.serialize(),compresslevel=1)); dst.close(); src.close()' "$1" "$2" "$3"
`
	cmd := r.command(ctx, snapshot, r.home, project, id)
	var data, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &diagnosticBoundedBuffer{buffer: &data, max: 32 << 20}, &diagnosticBoundedBuffer{buffer: &stderr, max: 4096}
	if err := cmd.Run(); err != nil {
		t.Fatalf("read accepted remote transcript: %v: %.2048s", err, stderr.String())
	}
	compressed, err := gzip.NewReader(&data)
	if err != nil {
		t.Fatal("invalid remote SQLite backup compression", err)
	}
	defer func() { _ = compressed.Close() }()
	decoded, err := io.ReadAll(io.LimitReader(compressed, (32<<20)+1))
	if err != nil || len(decoded) > 32<<20 {
		t.Fatal("remote SQLite backup exceeded decoded bound", err)
	}
	path := filepath.Join(t.TempDir(), "accepted-session.db")
	if err := os.WriteFile(path, decoded, 0600); err != nil {
		t.Fatal(err)
	}
	return diagnosticMessagesFromPath(t, path)
}

type diagnosticBoundedBuffer struct {
	buffer *bytes.Buffer
	max    int
}

func (b *diagnosticBoundedBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > b.max {
		return 0, fmt.Errorf("diagnostic test evidence exceeds %d bytes", b.max)
	}
	return b.buffer.Write(p)
}

// This uses all three real carriers against one retained server owner. It is
// independent of the model journey, so a prose report cannot fake transport use.
func TestLiveDiagnosticsRemoteCarriers(t *testing.T) {
	if os.Getenv("CRUDE_LIVE_DIAGNOSTICS") != "1" || os.Getenv("CRUDE_DIAGNOSTICS_REMOTE") == "" {
		t.Skip("explicit distinct-host acceptance")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	server := startLiveDiagnosticRemoteCore(t, ctx, false)
	r := server.remote
	ssh := func(context.Context) (corewire.Conn, error) {
		conn, err := corewire.DialSSHBuild(context.Background(), r.options, buildinfo.DataDirName, buildinfo.ProtocolBuild)
		if err != nil {
			return nil, err
		}
		return corewire.NewMux(conn, corewire.CarrierSSH, true), nil
	}
	c, err := coreclient.Dial(ctx, ssh, "diagnostics-carrier-acceptance")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	owner := c.Owner()
	bootstrap, err := c.BootstrapRemoteSSH(ctx, "disposable diagnostics")
	if err != nil {
		t.Fatal(err)
	}
	quic, err := sshDirectAddress(bootstrap.QUICAddress, r.options.Host)
	if err != nil {
		t.Fatal(err)
	}
	tls, err := sshDirectAddress(bootstrap.TLSAddress, r.options.Host)
	if err != nil {
		t.Fatal(err)
	}
	for _, next := range []struct {
		carrier corewire.Carrier
		dial    coreclient.Dialer
	}{
		{corewire.CarrierQUIC, func(ctx context.Context) (corewire.Conn, error) {
			return corewire.DialQUIC(ctx, quic, bootstrap.Credential.Fingerprint)
		}},
		{corewire.CarrierTCP, func(ctx context.Context) (corewire.Conn, error) {
			return corewire.DialTLS(ctx, tls, bootstrap.Credential.Fingerprint)
		}},
		{corewire.CarrierSSH, ssh},
	} {
		if err := c.SwitchCarrier(ctx, next.dial); err != nil {
			t.Fatal("real carrier switch", next.carrier, err)
		}
		carrier, _ := c.Carrier()
		if carrier != next.carrier || c.Owner() != owner {
			t.Fatal("carrier resume changed owner", carrier)
		}
		var page inspect.Page
		if err := c.Call(ctx, "", coreprotocol.CorePerformanceInspect, &coreprotocol.PerformanceInspectRequest{Page: inspect.Request{View: "series", Filter: "diagnostic_fixture", Limit: 1}}, &page); err != nil {
			t.Fatal(err)
		}
		var source struct {
			PID        int    `json:"pid"`
			HostBootID string `json:"host_boot_id"`
		}
		if json.Unmarshal(page.Source, &source) != nil || source.PID != server.pid || source.HostBootID == "" || page.Next == nil {
			t.Fatal("carrier lost metric provenance")
		}
		t.Logf("REMOTE_DIAGNOSTICS_CARRIER carrier=%s owner_preserved=true records=%d", carrier, page.Total)
	}
}
