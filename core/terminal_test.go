package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/core/coreclient"
	"crude/core/coreprotocol"
	"crude/core/corewire"
	"crude/core/shellport/remote"
	"crude/internal/testterminal"
	"crude/types/inspect"
	"crude/types/setting"
	"crude/workspace"
)

const attachEnvironmentProbe = "CRUDE_ATTACH_ENVIRONMENT_TEST"
const daemonEnvironmentProbe = "CRUDE_DAEMON_ENVIRONMENT_TEST"

func attachingEnvironmentRuntime(t *testing.T, logger workspace.Logger) *Runtime {
	t.Helper()
	setCoreTestHome(t, t.TempDir())
	executable, args := testterminal.Command(t, "env", attachEnvironmentProbe, daemonEnvironmentProbe)
	r, err := New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "tester", setting.Settings{}, logger)
		ws.SetAppDefaults(setting.App{Terminal: setting.Terminal{
			Shell: setting.TerminalShellCustom, Command: executable,
			Args: args,
		}})
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}

func attachingEnvironmentClient(t *testing.T, r *Runtime, carrier corewire.Carrier, id string) *coreclient.Client {
	t.Helper()
	server, client := net.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- ServeCarrier(ctx, r, corewire.NewMux(server, carrier, false), nil) }()
	// Both ends of the pipe use the same carrier framing (SSH is compressed).
	var once sync.Once
	dial := func(context.Context) (corewire.Conn, error) {
		var conn corewire.Conn
		once.Do(func() { conn = corewire.NewMux(client, carrier, true) })
		if conn == nil {
			return nil, coreclient.ErrNoCarrier
		}
		return conn, nil
	}
	c, err := coreclient.Dial(ctx, dial, id)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		cancel()
		select {
		case err := <-done:
			_ = err // a carrier closed by its test ends with its close error
		case <-time.After(5 * time.Second):
			t.Error("environment test carrier did not close")
		}
	})
	return c
}

func attachingEnvironmentTerminal(t *testing.T, c *coreclient.Client, workspaceID, shell, daemon string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var info workspace.TerminalInfo
	// No command from the shell: use the normal configured-terminal entry point.
	if err := c.Call(ctx, workspaceID, coreprotocol.TerminalCreate, &workspace.TerminalCreateOpts{Cols: 120, Rows: 6}, &info); err != nil {
		t.Fatal(err)
	}
	frames := make(chan workspace.TerminalProjection, 8)
	failures := make(chan error, 1)
	sub := c.SubscribeTerminal(ctx, workspaceID, info.ID, func(p workspace.TerminalProjection, err error) {
		if err != nil {
			select {
			case failures <- err:
			default:
			}
			return
		}
		select {
		case frames <- p:
		case <-ctx.Done():
		}
	})
	defer func() { cancel(); sub.Close() }()
	last := ""
	for {
		select {
		case p := <-frames:
			// The child prints both lines, then exits. Exit publication has its
			// own test (TestResourceEventsUnsolicitedOutputAndNaturalExit); here
			// the screen is the outcome, whether or not the exit frame came yet.
			text := projectionText(p)
			last = text
			if p.Info.Alive && !strings.Contains(text, daemonEnvironmentProbe+"=[") {
				continue
			}
			if !strings.Contains(text, attachEnvironmentProbe+"=["+shell+"]") || !strings.Contains(text, daemonEnvironmentProbe+"=["+daemon+"]") {
				t.Fatalf("terminal environment = %q, want shell=%q daemon=%q", text, shell, daemon)
			}
			if p.Info.ExitCode != 0 {
				t.Fatalf("environment child failed: %+v", p.Info)
			}
			return
		case err := <-failures:
			t.Fatal(err)
		case <-ctx.Done():
			t.Fatalf("terminal did not publish the child's environment; last screen %q", last)
		}
	}
}

func TestTerminalUsesTheAttachingShellEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name    string
		carrier corewire.Carrier
		absent  bool
	}{
		{name: "same-host shell", carrier: corewire.CarrierLocal},
		{name: "SSH shell sends no environment", carrier: corewire.CarrierSSH},
		{name: "absent environment", carrier: corewire.CarrierLocal, absent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := attachingEnvironmentRuntime(t, coreTestLogger{})
			root := t.TempDir()
			t.Setenv(attachEnvironmentProbe, "shell-only-value")
			t.Setenv(daemonEnvironmentProbe, "")
			var c *coreclient.Client
			var workspaceID string
			if tc.absent {
				c = attachingEnvironmentClient(t, r, tc.carrier, NewClientID())
				var attached coreprotocol.AttachResult
				if err := c.Call(t.Context(), "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
					t.Fatal(err)
				}
				workspaceID = attached.WorkspaceID
			} else {
				w, err := remote.Attach(t.Context(), remote.Options{Root: root, Dial: func(_ context.Context, id string) (*coreclient.Client, error) {
					c = attachingEnvironmentClient(t, r, tc.carrier, id)
					return c, nil
				}})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = w.Close() })
				workspaceID = w.WorkspaceID()
			}
			if c.LocalHost() != (tc.carrier == corewire.CarrierLocal) {
				t.Fatal("test transport did not establish the intended host identity")
			}
			// Now the variable exists only in the previously sent attach input,
			// not in the core's launch-time process environment.
			if err := os.Unsetenv(attachEnvironmentProbe); err != nil {
				t.Fatal(err)
			}
			t.Setenv(daemonEnvironmentProbe, "daemon-only-value")
			if tc.carrier == corewire.CarrierLocal && !tc.absent {
				attachingEnvironmentTerminal(t, c, workspaceID, "shell-only-value", "")
			} else {
				attachingEnvironmentTerminal(t, c, workspaceID, "", "daemon-only-value")
			}
		})
	}
}

func TestTerminalAttachingEnvironmentLatestLocalWins(t *testing.T) {
	r := attachingEnvironmentRuntime(t, coreTestLogger{})
	t.Setenv(attachEnvironmentProbe, "")
	t.Setenv(daemonEnvironmentProbe, "daemon-only-value")
	root := t.TempDir()
	one := attachingEnvironmentClient(t, r, corewire.CarrierLocal, "environment-one")
	two := attachingEnvironmentClient(t, r, corewire.CarrierLocal, "environment-two")
	ssh := attachingEnvironmentClient(t, r, corewire.CarrierSSH, "environment-ssh")
	attach := func(c *coreclient.Client, root string, env []string) string {
		t.Helper()
		var out coreprotocol.AttachResult
		if err := c.Call(t.Context(), "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root, Environment: env}, &out); err != nil {
			t.Fatal(err)
		}
		return out.WorkspaceID
	}
	workspaceID := attach(one, root, []string{attachEnvironmentProbe + "=first"})
	attachingEnvironmentTerminal(t, one, workspaceID, "first", "")
	attach(two, root, []string{attachEnvironmentProbe + "=second"})
	if err := two.Close(); err != nil {
		t.Fatal(err)
	}
	attach(ssh, root, nil)
	attach(one, root, nil)
	attachingEnvironmentTerminal(t, one, workspaceID, "second", "")
	other := attach(one, t.TempDir(), nil)
	attachingEnvironmentTerminal(t, one, other, "", "daemon-only-value")
	attach(one, root, []string{})
	attachingEnvironmentTerminal(t, one, workspaceID, "", "")
}

type attachEnvironmentLogger struct {
	mu   sync.Mutex
	text strings.Builder
}

func (l *attachEnvironmentLogger) record(category, message string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(&l.text, category, message, args)
}
func (l *attachEnvironmentLogger) Info(c, m string, a ...any)  { l.record(c, m, a...) }
func (l *attachEnvironmentLogger) Warn(c, m string, a ...any)  { l.record(c, m, a...) }
func (l *attachEnvironmentLogger) Error(c, m string, a ...any) { l.record(c, m, a...) }
func (l *attachEnvironmentLogger) Debug(c, m string, a ...any) { l.record(c, m, a...) }

func TestAttachEnvironmentStaysOutOfReceiptsLogsAndDiagnostics(t *testing.T) {
	logger := &attachEnvironmentLogger{}
	r := attachingEnvironmentRuntime(t, logger)
	c, err := r.Connect("private-environment")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	c.localHost = true // Direct owner fixture; wire identity is tested above.
	const secret = "attach-environment-private-sentinel"
	payload, err := coreprotocol.Marshal(&coreprotocol.AttachRequest{Root: t.TempDir(), Environment: []string{attachEnvironmentProbe + "=" + secret}})
	if err != nil {
		t.Fatal(err)
	}
	request := coreprotocol.Request{ID: "private-attach", Owner: c.Owner, Method: coreprotocol.Attach, Payload: payload}
	cmd, err := c.admit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-cmd.done:
	case <-time.After(10 * time.Second):
		t.Fatal("attach did not finish")
	}
	if cmd.response.Error != nil {
		t.Fatal(cmd.response.Error)
	}
	if _, err := r.receipts.lookup(t.Context(), c.Owner.ClientID, request.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("attach unexpectedly used the durable receipt journal: %v", err)
	}
	for _, view := range []string{"summary", "requests", "workspaces", "clients"} {
		observed := r.collectDiagnostics(t.Context(), c, "", inspect.Request{View: view})
		raw, err := json.Marshal(observed)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), secret) || strings.Contains(string(raw), attachEnvironmentProbe) {
			t.Fatalf("%s diagnostics exposed the attaching environment", view)
		}
	}
	if strings.Contains(string(cmd.response.Payload), secret) || strings.Contains(string(cmd.response.Payload), attachEnvironmentProbe) {
		t.Fatal("attach snapshot exposed the attaching environment")
	}
	logger.mu.Lock()
	logged := logger.text.String()
	logger.mu.Unlock()
	if strings.Contains(logged, secret) || strings.Contains(logged, attachEnvironmentProbe) {
		t.Fatal("attach logged the attaching environment")
	}
	c.forget(cmd)
}

func TestAttachEnvironmentBoundsAndRemoteRefusal(t *testing.T) {
	r := attachingEnvironmentRuntime(t, coreTestLogger{})
	local := attachingEnvironmentClient(t, r, corewire.CarrierLocal, "environment-limits")
	remote := attachingEnvironmentClient(t, r, corewire.CarrierSSH, "environment-refused")
	entries := make([]string, coreprotocol.MaxAttachEnvironmentEntries+1)
	for i := range entries {
		entries[i] = "KEY=value"
	}
	for _, tc := range []struct {
		name          string
		client        *coreclient.Client
		env           []string
		code, message string
	}{
		{name: "count", client: local, env: entries, code: "invalid_request", message: "4096 entries"},
		{name: "size", client: local, env: []string{"SECRET=" + strings.Repeat("x", coreprotocol.MaxAttachEnvironmentBytes)}, code: "invalid_request", message: "1 MiB"},
		{name: "NUL", client: local, env: []string{"SECRET=private\x00sentinel"}, code: "invalid_request", message: "NUL bytes"},
		{name: "entry", client: local, env: []string{"private-sentinel"}, code: "invalid_request", message: "NAME=value"},
		{name: "remote", client: remote, env: []string{"SECRET=private-sentinel"}, code: "forbidden", message: "Remote shells"},
		{name: "remote empty", client: remote, env: []string{}, code: "forbidden", message: "Remote shells"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.client.Call(t.Context(), "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: t.TempDir(), Environment: tc.env}, nil)
			var fault *coreprotocol.Fault
			if !errors.As(err, &fault) || fault.Code != tc.code || !strings.Contains(fault.Message, tc.message) {
				t.Fatalf("attach refusal = %v, want %s / %s", err, tc.code, tc.message)
			}
			if strings.Contains(fault.Message, "SECRET") || strings.Contains(fault.Message, "private") {
				t.Fatal("attach error disclosed environment content")
			}
		})
	}
	r.mu.Lock()
	created := len(r.workspaces)
	r.mu.Unlock()
	if created != 0 {
		t.Fatalf("refused environment created %d workspace actors", created)
	}
	for _, env := range [][]string{nil, {}, {"KEY=value"}, {"=C:=C:\\work"}, {"K=" + strings.Repeat("x", coreprotocol.MaxAttachEnvironmentBytes-3)}, entries[:coreprotocol.MaxAttachEnvironmentEntries]} {
		request := coreprotocol.AttachRequest{Root: "test", Environment: env}
		if err := request.ValidateEnvironment(); err != nil {
			t.Fatalf("valid environment refused: %v", err)
		}
		raw, err := coreprotocol.Marshal(&request)
		if err != nil {
			t.Fatal(err)
		}
		var decoded coreprotocol.AttachRequest
		if err := coreprotocol.Decode(raw, &decoded); err != nil || !reflect.DeepEqual(decoded.Environment, env) {
			t.Fatalf("attach environment round trip lost absence or values: %v", err)
		}
	}
}
