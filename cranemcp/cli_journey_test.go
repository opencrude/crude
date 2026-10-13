package cranemcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"crude/buildinfo"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/types/control"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type recordedReader struct {
	io.ReadCloser
	output bytes.Buffer
}

func (r *recordedReader) Read(b []byte) (int, error) {
	n, err := r.ReadCloser.Read(b)
	_, _ = r.output.Write(b[:n])
	return n, err
}

type cliProcess struct {
	session *mcp.ClientSession
	cmd     *exec.Cmd
	done    chan error
	out     *recordedReader
	stderr  bytes.Buffer
	joined  bool
}

func (p *cliProcess) join(t *testing.T, wantSuccess bool) {
	t.Helper()
	if p.joined {
		return
	}
	select {
	case err := <-p.done:
		p.joined = true
		if wantSuccess && err != nil {
			t.Errorf("MCP process: %v: %s", err, p.stderr.String())
		}
	case <-time.After(5 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
		p.joined = true
		t.Error("MCP subprocess failed to exit within five seconds")
	}
	// Session.Close joins the SDK's reader before inspecting its recording.
	_ = p.session.Close()
	for _, line := range strings.Split(strings.TrimSpace(p.out.output.String()), "\n") {
		var msg struct {
			JSONRPC string `json:"jsonrpc"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil || msg.JSONRPC != "2.0" {
			t.Errorf("stdout contains non-protocol output: %q", line)
		}
	}
}

func cliSDK(t *testing.T, binary string, env []string, root string, flags ...string) *cliProcess {
	t.Helper()
	p := &cliProcess{done: make(chan error, 1)}
	p.cmd = exec.Command(binary, append([]string{"--workspace", root, "--mcp"}, flags...)...)
	p.cmd.Env = env
	p.cmd.Stderr = &p.stderr
	in, err := p.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p.out = &recordedReader{ReadCloser: out}
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.done <- p.cmd.Wait() }()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	p.session, err = mcp.NewClient(&mcp.Implementation{Name: "CLI acceptance", Version: "1"}, nil).Connect(ctx, &mcp.IOTransport{Reader: p.out, Writer: in}, nil)
	if err != nil {
		_ = p.cmd.Process.Kill()
		<-p.done
		t.Fatalf("SDK initialize: %v: %s", err, p.stderr.String())
	}
	t.Cleanup(func() { _ = p.session.Close(); p.join(t, false) })
	return p
}

func cliCall(t *testing.T, p *cliProcess, name string, args any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	out, err := p.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func cliOutcome(t *testing.T, out *mcp.CallToolResult) control.Result {
	t.Helper()
	raw, err := json.Marshal(out.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var result control.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// This builds the actual entry point, not a helper-process approximation of
// app.RunMCP. All owned processes and configuration live in a throwaway profile.
func TestCLIProtocolJourney(t *testing.T) {
	if testing.Short() {
		t.Skip("real CLI subprocess acceptance")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	binary := os.Getenv("CRUDE_MCP_ACCEPTANCE_BINARY")
	if binary == "" {
		binary = filepath.Join(t.TempDir(), "crude")
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		build := exec.CommandContext(ctx, "go", "build", "-o", binary, "..")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build CLI: %v\n%s", err, output)
		}
	}
	home, root := t.TempDir(), t.TempDir()
	// Explicitly remove inherited live-profile and profiling controls.
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CRUDE_HOME=") && !strings.HasPrefix(entry, "CRUDE_PPROF=") && !strings.HasPrefix(entry, "CRUDE_PERF_LOG=") {
			env = append(env, entry)
		}
	}
	env = append(env, "CRUDE_HOME="+home)
	coreCmd := exec.Command(binary, "--core", "--server")
	coreCmd.Env = env
	var diagnostics bytes.Buffer
	coreCmd.Stderr = &diagnostics
	if err := coreCmd.Start(); err != nil {
		t.Fatal(err)
	}
	coreDone := make(chan error, 1)
	go func() { coreDone <- coreCmd.Wait() }()
	coreJoined := false
	t.Cleanup(func() {
		if !coreJoined {
			_ = coreCmd.Process.Kill()
			<-coreDone
		}
	})
	var shell *coreclient.Client
	for {
		conn, err := corewire.DialLocal(ctx, filepath.Join(home, buildinfo.DataDir()))
		if err == nil {
			shell, err = coreclient.Dial(ctx, conn, "cli-shell-a")
			if err != nil {
				_ = conn.Close()
				t.Fatal(err)
			}
			break
		}
		select {
		case err := <-coreDone:
			coreJoined = true
			t.Fatalf("core startup: %v: %s", err, diagnostics.String())
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Cleanup(func() { _ = shell.Close() })
	go func() {
		for range shell.Events() {
		}
	}()
	var attached coreprotocol.AttachResult
	if err := shell.Call(ctx, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
		t.Fatal(err)
	}
	if err := shell.RegisterControlShell(ctx, attached.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	peerConn, err := corewire.DialLocal(ctx, filepath.Join(home, buildinfo.DataDir()))
	if err != nil {
		t.Fatal(err)
	}
	peer, err := coreclient.Dial(ctx, peerConn, "cli-shell-b")
	if err != nil {
		_ = peerConn.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	go func() {
		for range peer.Events() {
		}
	}()
	var peerAttached coreprotocol.AttachResult
	if err := peer.Call(ctx, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &peerAttached); err != nil {
		t.Fatal(err)
	}
	if peerAttached.WorkspaceID != attached.WorkspaceID {
		t.Fatal("peer attached another workspace")
	}
	if err := peer.RegisterControlShell(ctx, attached.WorkspaceID); err != nil {
		t.Fatal(err)
	}

	reader := cliSDK(t, binary, env, root)
	tools, err := reader.session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 4 {
		t.Fatalf("tools: %#v %v", tools, err)
	}
	if out := cliCall(t, reader, "crude_commands", map[string]any{"query": "settings"}); out.IsError || out.StructuredContent == nil {
		t.Fatal(out)
	}
	if out := cliCall(t, reader, "crude_shells", map[string]any{}); out.IsError || !strings.Contains(fmt.Sprint(out.StructuredContent), "cli-shell-a") {
		t.Fatal(out)
	}
	getArgs := map[string]any{"command": "settings.get", "arguments": map[string]any{"scope": "application"}}
	get := cliOutcome(t, cliCall(t, reader, "crude_control", getArgs))
	var settings struct {
		Revision    string `json:"application_revision"`
		Application struct {
			Editor struct {
				WordWrap bool `json:"word_wrap"`
				TabSize  int  `json:"tab_size"`
			} `json:"editor"`
		} `json:"application"`
	}
	if err := json.Unmarshal(get.Output, &settings); err != nil || settings.Revision == "" {
		t.Fatalf("settings preimage: %s %v", get.Output, err)
	}
	originalWrap, originalTab := settings.Application.Editor.WordWrap, settings.Application.Editor.TabSize
	setArgs := map[string]any{"command": "settings.set", "arguments": map[string]any{"scope": "application", "path": "editor.word_wrap", "value": !originalWrap, "expected_revision": settings.Revision}}
	if out := cliCall(t, reader, "crude_control", setArgs); !out.IsError {
		t.Fatal("read-only subprocess admitted write")
	}
	writer := cliSDK(t, binary, env, root, "--mcp-effects", "read,write", "--mcp-shell", shell.Owner().ClientID, "--mcp-shell-epoch", strconv.FormatUint(shell.Owner().Epoch, 10))
	changed := cliOutcome(t, cliCall(t, writer, "crude_control", setArgs))
	if changed.State != control.Committed || changed.RequestID == "" {
		t.Fatalf("mutation: %#v", changed)
	}
	if out := cliCall(t, writer, "crude_control", setArgs); !out.IsError {
		t.Fatal("stale settings revision admitted")
	}
	if out := cliCall(t, writer, "crude_receipt", map[string]any{"request_id": changed.RequestID}); out.IsError || !strings.Contains(fmt.Sprint(out.StructuredContent), changed.RequestID) {
		t.Fatal(out)
	}
	get = cliOutcome(t, cliCall(t, reader, "crude_control", getArgs))
	if err := json.Unmarshal(get.Output, &settings); err != nil || settings.Application.Editor.WordWrap == originalWrap || settings.Application.Editor.TabSize != originalTab {
		t.Fatalf("settings not published: %s %v", get.Output, err)
	}
	persisted, err := os.ReadFile(filepath.Join(home, buildinfo.DataDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Editor struct {
			WordWrap bool `json:"word_wrap"`
		} `json:"editor"`
	}
	if err := json.Unmarshal(persisted, &config); err != nil || config.Editor.WordWrap == originalWrap {
		t.Fatalf("settings not persisted: %s %v", persisted, err)
	}

	replied := make(chan string, 1)
	go func() {
		select {
		case envelope := <-shell.ControlRequests():
			_ = shell.ReplyControl(ctx, attached.WorkspaceID, control.Result{RequestID: envelope.Request.ID, OperationID: envelope.Request.ID, State: control.Applied, Output: json.RawMessage(`{"owner":"cli-shell-a"}`)})
			replied <- envelope.Request.ID
		case <-ctx.Done():
		}
	}()
	inspected := cliOutcome(t, cliCall(t, writer, "crude_control", map[string]any{"command": "shell.inspect", "arguments": map[string]any{}}))
	if inspected.State != control.Applied || string(inspected.Output) != `{"owner":"cli-shell-a"}` {
		t.Fatalf("exact shell: %#v", inspected)
	}
	select {
	case id := <-replied:
		if id != inspected.RequestID {
			t.Fatal("shell/request identity changed")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if out := cliCall(t, reader, "crude_control", map[string]any{"command": "shell.inspect", "arguments": map[string]any{}}); !out.IsError {
		t.Fatal("unbound adapter inferred a shell")
	}
	_ = reader.session.Close()
	reader.join(t, true)

	// Disconnect the exact target only after its request has arrived. No reply
	// is manufactured and no second shell may receive a replay.
	pending := make(chan *mcp.CallToolResult, 1)
	pendingErr := make(chan error, 1)
	go func() {
		out, err := writer.session.CallTool(ctx, &mcp.CallToolParams{Name: "crude_control", Arguments: map[string]any{"command": "shell.inspect", "arguments": map[string]any{}}})
		pending <- out
		pendingErr <- err
	}()
	var admittedID string
	select {
	case envelope := <-shell.ControlRequests():
		admittedID = envelope.Request.ID
	case <-time.After(5 * time.Second):
		t.Fatal("pending shell request was not delivered")
	}
	_ = shell.Close()
	select {
	case out := <-pending:
		if err := <-pendingErr; err != nil {
			t.Fatal(err)
		}
		result := cliOutcome(t, out)
		if !out.IsError || result.RequestID != admittedID || (result.State != control.Uncertain && result.State != control.Failed) {
			t.Fatalf("target disconnect lost identity or reported success: %#v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("target disconnect did not settle pending SDK request")
	}
	select {
	case envelope := <-peer.ControlRequests():
		t.Fatalf("request rerouted/replayed to unrelated shell: %s", envelope.Request.ID)
	default:
	}
	adminConn, err := corewire.DialLocal(ctx, filepath.Join(home, buildinfo.DataDir()))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := coreclient.Dial(ctx, adminConn, "shutdown-owner")
	if err != nil {
		_ = adminConn.Close()
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()

	// Core loss must terminate the idle stdio subprocess even with stdin open.
	var shutdown coreprotocol.ShutdownResult
	if err := admin.Call(ctx, "", coreprotocol.CoreShutdownMethod, coreprotocol.ShutdownRequest{Force: true}, &shutdown); err != nil || !shutdown.Accepted {
		t.Fatalf("shutdown: %#v %v", shutdown, err)
	}
	select {
	case err := <-coreDone:
		coreJoined = true
		if err != nil {
			t.Fatalf("core exit: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("core failed to exit")
	}
	writer.join(t, false)
	conn, err := corewire.DialLocal(ctx, filepath.Join(home, buildinfo.DataDir()))
	if err == nil {
		_ = conn.Close()
		t.Fatal("owned core endpoint still live after shutdown")
	}
}
