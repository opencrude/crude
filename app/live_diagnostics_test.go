package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/metrics"
	"crude/network"
	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	"crude/shellport/remote"
	"crude/types"
	"crude/types/control"
	"crude/types/inspect"
	"crude/workspace"
)

const liveDiagnosticsModel = "deepseek/deepseek-v4.1-flash"

// This is a test-binary role, never a product command or an SSH shell escape.
// TestMain's testhome isolation runs first; restore only our explicit private home.
func TestLiveDiagnosticsCoreHelper(t *testing.T) {
	if os.Getenv("CRUDE_DIAGNOSTICS_HELPER") != "1" {
		t.Skip("private core subprocess role")
	}
	home := os.Getenv("CRUDE_DIAGNOSTICS_HOME")
	if home == "" {
		t.Fatal("private helper home required")
	}
	t.Setenv("CRUDE_HOME", home)
	fc := DefaultFileConfig()
	if os.Getenv("CRUDE_DIAGNOSTICS_HELPER_LIVE") == "1" {
		var err error
		fc, err = loadFileConfigFrom(os.Getenv("CRUDE_DIAGNOSTICS_CONFIG"))
		if err != nil {
			t.Fatal("cannot read explicitly configured account")
		}
		cfg := FileConfigToSession(fc)
		main := cfg.GetActiveDefaultCategory()
		if account := os.Getenv("CRUDE_DIAGNOSTICS_ACCOUNT"); account != "" {
			main.AccountID = account
		}
		account, ok := cfg.Accounts[main.AccountID]
		if !ok || account.ProviderID == "" {
			t.Fatal("explicitly configured diagnostic account unavailable")
		}
		main.Model, main.ThinkingEffort, main.Policy = liveDiagnosticsModel, "low", "read-only"
		main.AllowedTools = []string{"crude_commands", "crude_control", "read_file", "reply"}
		main.DeniedTools = nil
		profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
		off := false
		profile.MultiAgent, profile.TurnLogs = &off, &off
		profile.Categories[session.CategoryDefault] = main
		// The live compaction path must not silently exercise a stronger or
		// unrelated extraction model than the task model under acceptance.
		profile.Categories[session.CategoryAeron] = main
		cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
		cfg.Memory.AutoRecall, cfg.Memory.AutoExtract, cfg.Memory.Consolidation = "off", "off", "off"
		// RunCore owns configuration loading. Its sole credential-bearing copy is
		// this 0600 config inside the disposable local profile, never evidence.
		fc.Accounts = map[string]provider.Account{main.AccountID: account}
		fc.Agents, fc.Memory, fc.TurnLogsEnabled = &cfg.Agents, &cfg.Memory, &off
		fc.Capabilities = nil
	}
	if fc.Network != nil {
		network := *fc.Network
		network.RemoteAccessEnabled = false // never duplicate real listener policy
		fc.Network = &network
	}
	if os.Getenv("CRUDE_DIAGNOSTICS_HELPER_REMOTE") == "1" {
		policy := network.DefaultSettings()
		policy.RemoteAccessEnabled = true
		policy.RemoteQUICAddress = os.Getenv("CRUDE_DIAGNOSTICS_REMOTE_QUIC")
		policy.RemoteTLSAddress = os.Getenv("CRUDE_DIAGNOSTICS_REMOTE_TLS")
		if err := policy.Validate(); err != nil {
			t.Fatal("invalid private remote listener policy", err)
		}
		fc.Network = &policy
	}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal("cannot write private helper configuration")
	}
	// Real process-owned registry entries, deliberately labelled synthetic. The
	// gateway must still collect them through its production metric owner.
	for i := range 80 {
		metrics.NewLatency(metrics.Meta{Name: fmt.Sprintf("diagnostic_fixture.%03d", i), Group: "diagnostic_fixture", Title: "Synthetic diagnostic workload", Warn: 1000, Crit: 2000}).Observe(3 * time.Millisecond)
	}
	if err := RunCore(CoreOptions{Home: home, Server: true}); err != nil {
		t.Fatal("private diagnostic core failed", err)
	}
}

type liveDiagnosticCore struct {
	home   string
	cmd    *exec.Cmd
	done   chan error
	pid    int
	remote *liveDiagnosticRemote
}

func startLiveDiagnosticCore(t *testing.T, ctx context.Context, home string, live bool) *liveDiagnosticCore {
	t.Helper()
	if os.Getenv("CRUDE_DIAGNOSTICS_REMOTE") != "" {
		return startLiveDiagnosticRemoteCore(t, ctx, live)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	log, err := os.CreateTemp(t.TempDir(), "diagnostic-core-*.log")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	cmd := exec.Command(exe, "-test.run=^TestLiveDiagnosticsCoreHelper$", "-test.timeout=15m")
	// Values are paths/role flags, never secret arguments or credential values.
	cmd.Env = append(os.Environ(), "CRUDE_APP_TEST_MAIN=0", "CRUDE_DIAGNOSTICS_HELPER=1", "CRUDE_DIAGNOSTICS_HOME="+home, "CRUDE_DIAGNOSTICS_HELPER_LIVE="+strconv.Itoa(boolIntDiagnostic(live)))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	server := &liveDiagnosticCore{home: home, cmd: cmd, done: make(chan error, 1), pid: cmd.Process.Pid}
	go func() { server.done <- cmd.Wait() }()
	t.Cleanup(func() { server.stop(t) })
	ready := time.NewTicker(50 * time.Millisecond)
	defer ready.Stop()
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		probe, stop := context.WithTimeout(deadline, time.Second)
		status, err := CoreStatus(probe, home)
		stop()
		if err == nil {
			if status.PID != server.pid || status.PID == os.Getpid() || !status.Server {
				t.Fatal("diagnostics connected to an unexpected core process")
			}
			return server
		}
		select {
		case err := <-server.done:
			server.cmd = nil
			t.Fatalf("private core exited before readiness: %v (private log %s)", err, log.Name())
		case <-deadline.Done():
			t.Fatalf("private core readiness: %v (private log %s)", deadline.Err(), log.Name())
		case <-ready.C:
		}
	}
}

func boolIntDiagnostic(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *liveDiagnosticCore) stop(t *testing.T) {
	t.Helper()
	if s.remote != nil {
		s.remote.stop(t, s.pid)
		s.remote = nil
		return
	}
	if s.cmd == nil {
		return
	}
	// Only the private endpoint and our exact retained process can be stopped.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	result, err := StopCore(ctx, s.home, true)
	cancel()
	if err != nil || !result.Accepted {
		t.Logf("private core stop receipt unavailable; joining exact helper pid=%d", s.pid)
	}
	select {
	case err := <-s.done:
		if err != nil {
			t.Errorf("private core exit: %v", err)
		}
	case <-time.After(15 * time.Second):
		_ = s.cmd.Process.Kill()
		<-s.done
		t.Error("private core required exact-process kill after stop timeout")
	}
	s.cmd = nil
}

func liveDiagnosticHome(t *testing.T) string {
	t.Helper()
	parent := ""
	if runtime.GOOS != "windows" {
		parent = "/tmp" // real short socket path; not a symlink to long evidence paths
	}
	home, err := os.MkdirTemp(parent, "crdiag-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("CRUDE_HOME", home)
	return home
}

func diagnosticDialer(home string) remote.Dialer {
	if os.Getenv("CRUDE_DIAGNOSTICS_REMOTE") != "" {
		return liveDiagnosticRemoteDialer(home)
	}
	return func(ctx context.Context, id string) (*coreclient.Client, error) {
		return dialCore(ctx, DataDir(home), id)
	}
}

// No inference, configured credential read, SSH, or TUI is needed for this smoke.
func TestDiagnosticsHarnessDistinctCoreSmoke(t *testing.T) {
	duration := time.Minute
	if os.Getenv("CRUDE_VISUAL_DIAGNOSTICS") == "1" {
		duration = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(t.Context(), duration)
	defer cancel()
	home := liveDiagnosticHome(t)
	server := startLiveDiagnosticCore(t, ctx, home, false)
	project := t.TempDir()
	one, err := remote.Attach(ctx, remote.Options{Root: project, Dial: diagnosticDialer(home)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = one.Close() }()
	two, err := remote.Attach(ctx, remote.Options{Root: project, Dial: diagnosticDialer(home)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = two.Close() }()
	a, b := one.ControlIdentity(), two.ControlIdentity()
	if a.WorkspaceID != b.WorkspaceID || a.Shell == nil || b.Shell == nil || *a.Shell == *b.Shell {
		t.Fatal("remote ports did not retain independent owners in one workspace")
	}
	client, err := diagnosticDialer(home)(ctx, "diagnostics-smoke-observer")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	var page inspect.Page
	if err := client.Call(ctx, "", coreprotocol.CorePerformanceInspect, &coreprotocol.PerformanceInspectRequest{Page: inspect.Request{View: "series", Filter: "diagnostic_fixture", Limit: 1, MaxBytes: 4096}}, &page); err != nil {
		t.Fatal(err)
	}
	var source struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(page.Source, &source); err != nil || source.PID != server.pid || page.Total != 80 || page.Next == nil {
		t.Fatalf("actual owner/continuation missing: pid=%d total=%d next=%v err=%v", source.PID, page.Total, page.Next, err)
	}
	var next inspect.Page
	if err := client.Call(ctx, "", coreprotocol.CorePerformanceInspect, &coreprotocol.PerformanceInspectRequest{Page: *page.Next}, &next); err != nil || next.SnapshotID != page.SnapshotID || next.Offset != 1 {
		t.Fatal("bounded immutable continuation failed", err)
	}
	invalid := *page.Next
	invalid.Offset = page.Total + 1
	if err := client.Call(ctx, "", coreprotocol.CorePerformanceInspect, &coreprotocol.PerformanceInspectRequest{Page: invalid}, &next); err == nil {
		t.Fatal("out-of-range diagnostic continuation was accepted")
	}
	if err := client.Call(ctx, "", coreprotocol.CorePerformanceInspect, &coreprotocol.PerformanceInspectRequest{Page: *page.Next}, &next); err != nil {
		t.Fatal("valid continuation unavailable after rejected range", err)
	}
	// Exercise the actual shell handlers without inference, not a hand-built
	// reply. The second port above stays detached from this exact shell target.
	shell := startLiveDiagnosticShell(t, ctx, home, project, "")
	defer shell.close()
	var attached coreprotocol.AttachResult
	if err := client.Call(ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: project}, &attached); err != nil {
		t.Fatal(err)
	}
	target := shell.port.ControlIdentity()
	if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.ControlClientRegister, &coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read}, Shell: target.Shell}, nil); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"performance.shell", "connection.inspect", "diagnostics.core", "diagnostics.workspace"} {
		id := "smoke-" + command
		var result control.Result
		request := &control.Request{ID: id, CommandID: command, Target: target, Input: json.RawMessage(`{}`)}
		if _, err := client.CallWith(ctx, attached.WorkspaceID, coreprotocol.ControlExecute, request, &result, coreclient.CallOptions{ID: id}); err != nil || result.Fault != nil {
			t.Fatalf("production %s handler failed: %v %v", command, err, result.Fault)
		}
		if command == "performance.shell" {
			var page inspect.Page
			var source struct {
				Process string `json:"process"`
				PID     int    `json:"pid"`
			}
			if json.Unmarshal(result.Output, &page) != nil || json.Unmarshal(page.Source, &source) != nil || source.PID != os.Getpid() || source.Process != "shell" || source.PID == server.pid {
				t.Fatal("production shell handler substituted core provenance")
			}
		}
	}
	shell.hold(t, ctx)
}

// Opt in explicitly. No fallback model, real-profile core, or SSH autostart.
// Phases basic|dense|restart|all are independently runnable within 14 minutes.
func TestLiveDiagnosticsActualModel(t *testing.T) {
	if os.Getenv("CRUDE_LIVE_DIAGNOSTICS") != "1" {
		t.Skip("set CRUDE_LIVE_DIAGNOSTICS=1 and CRUDE_DIAGNOSTICS_CONFIG")
	}
	if os.Getenv("CRUDE_DIAGNOSTICS_CONFIG") == "" {
		t.Fatal("CRUDE_DIAGNOSTICS_CONFIG must explicitly name the configured account file")
	}
	phase := os.Getenv("CRUDE_DIAGNOSTICS_PHASE")
	if phase == "" {
		phase = "basic"
	}
	if phase != "basic" && phase != "dense" && phase != "restart" && phase != "all" {
		t.Fatal("CRUDE_DIAGNOSTICS_PHASE must be basic, dense, restart, or all")
	}
	duration := 14 * time.Minute
	if os.Getenv("CRUDE_DIAGNOSTICS_SOAK") == "1" {
		duration = 24 * time.Minute
	}
	ctx, cancel := context.WithTimeout(t.Context(), duration)
	defer cancel()
	home := liveDiagnosticHome(t)
	server := startLiveDiagnosticCore(t, ctx, home, true)
	project := t.TempDir()
	if server.remote != nil {
		project = filepath.Join(server.remote.home, "project")
	} else if err := os.WriteFile(filepath.Join(project, "ordinary.txt"), []byte("ORDINARY_DIAGNOSTIC_7Q\n"), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := diagnosticDialer(home)(ctx, "diagnostics-model-observer")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	var attached coreprotocol.AttachResult
	if err := client.Call(ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: project}, &attached); err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 32)
	followLiveAgentCompletions(t, client, attached.WorkspaceID, done)
	var created workspace.NewSessionResult
	if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.NewSession, &coreprotocol.InvocationSessionRequest{Selection: coreprotocol.InvocationSelection{Model: liveDiagnosticsModel}}, &created); err != nil || created.Error != "" {
		t.Fatal("exact diagnostic model session unavailable", err, created.Error)
	}
	var snapshot coreprotocol.WorkspaceSnapshot
	if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.Snapshot, &coreprotocol.Empty{}, &snapshot); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ag := range snapshot.Agents {
		if ag.ID == created.OrchID {
			found = ag.Model == liveDiagnosticsModel
		}
	}
	if !found {
		t.Fatal("session did not preserve exact requested model; refusing fallback")
	}
	shell := startLiveDiagnosticShell(t, ctx, home, project, created.OrchID)
	peer, err := remote.Attach(ctx, remote.Options{Root: project, Dial: diagnosticDialer(home)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	if shell.port.ControlIdentity().Shell.ClientID == peer.ControlIdentity().Shell.ClientID {
		t.Fatal("peer shell owner was not distinct")
	}
	goal := "Inspect this IDE's performance and operational state using its supported self-control tools. Get exact client and server evidence, including their process identities, the current connection, retained connections, and canonical workspace health (issues, operations, notifications and agent status, not only actor capacity). Use bounded continuation when required. Do not open dashboards, repair anything, run commands, modify files, or poll repeatedly. Explain the relevant findings, distinguish synthetic diagnostic_fixture workload from real measurements, and distinguish link responsiveness from replica freshness and provider health. Include exact observed PIDs in your concise final report."
	shell.submit(t, ctx, goal)
	waitLiveDiagnosticTurn(t, ctx, done, created.OrchID)
	rows := liveDiagnosticMessages(t, project, created.SessionID)
	assertLiveDiagnosticEvidence(t, rows, server.pid, shell.port.ControlIdentity())
	before := len(rows)
	shell.submit(t, ctx, "Read ordinary.txt and report its first line exactly. This is ordinary file work; do not inspect performance, connection, or operational diagnostics.")
	waitLiveDiagnosticTurn(t, ctx, done, created.OrchID)
	rows = liveDiagnosticMessages(t, project, created.SessionID)
	ordinary := rows[min(before, len(rows)):]
	if !strings.Contains(lastDiagnosticAnswer(ordinary), "ORDINARY_DIAGNOSTIC_7Q") {
		t.Error("ordinary read did not report actual fixture content")
	}
	for _, row := range ordinary {
		for _, call := range row.Msg.ToolCalls {
			if call.Function.Name == "crude_control" || call.Function.Name == "crude_commands" {
				t.Error("ordinary work polled unrelated self-control diagnostics")
			}
		}
	}
	if phase == "dense" || phase == "all" {
		t.Log("live phase: dense paste, exact continuation and out-of-range recovery")
		before = len(rows)
		text := denseDiagnosticContext() + "\nInspect the core synthetic diagnostic_fixture performance series using small bounded pages; follow at least one continuation of the same observation. Exercise one deliberately out-of-range continuation, then recover from its tool error using the supported guidance and complete a valid read. Report the synthetic observation and the context marker DENSE_DIAGNOSTICS_END_9Q. Do not dump all series or repeat background polling."
		shell.submit(t, ctx, text)
		waitLiveDiagnosticTurn(t, ctx, done, created.OrchID)
		rows = liveDiagnosticMessages(t, project, created.SessionID)
		assertDenseDiagnosticEvidence(t, rows[min(before, len(rows)):])
	}
	if phase == "restart" || phase == "all" {
		t.Log("live phase: completed-session core restart and passive resume")
		// Graceful completed-turn shutdown; this does not claim an uninterrupted
		// model turn across core death. Resume itself must remain passive.
		shell.close()
		_ = peer.Close()
		_ = client.Close()
		server.stop(t)
		server = startLiveDiagnosticCore(t, ctx, home, true)
		client, err = diagnosticDialer(home)(ctx, "diagnostics-restarted-observer")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close() }()
		if err := client.Call(ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: project}, &attached); err != nil {
			t.Fatal(err)
		}
		var reopened workspace.NewSessionResult
		if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.OpenSession, &coreprotocol.InvocationSessionRequest{SessionID: created.SessionID}, &reopened); err != nil || reopened.Error != "" {
			t.Fatal("passive session reopen failed", err, reopened.Error)
		}
		afterResume := liveDiagnosticMessages(t, project, created.SessionID)
		if !reflect.DeepEqual(afterResume, rows) {
			t.Fatal("passive resume changed accepted transcript")
		}
		done = make(chan string, 32)
		followLiveAgentCompletions(t, client, attached.WorkspaceID, done)
		shell = startLiveDiagnosticShell(t, ctx, home, project, reopened.OrchID)
		shell.submit(t, ctx, "The core was deliberately stopped and restarted after your completed answer. Inspect its current performance and the exact current shell connection via supported self-control. Report the newly observed core PID; do not reuse the old PID or claim uninterrupted model execution.")
		waitLiveDiagnosticTurn(t, ctx, done, reopened.OrchID)
		fresh := liveDiagnosticMessages(t, project, created.SessionID)
		if !strings.Contains(lastDiagnosticAnswer(fresh[len(afterResume):]), strconv.Itoa(server.pid)) {
			t.Error("restart followup did not report the replacement process")
		}
		rows = fresh
		created.OrchID = reopened.OrchID
	}
	if os.Getenv("CRUDE_DIAGNOSTICS_SOAK") == "1" {
		liveDiagnosticSoak(t, ctx, shell, client, attached.WorkspaceID, project, created, done, rows, server.pid)
	}
	if !t.Failed() {
		if path := os.Getenv("CRUDE_DIAGNOSTICS_RESULT"); path != "" {
			if err := os.WriteFile(path, []byte("DIAGNOSTICS_SUCCESS model="+liveDiagnosticsModel+" phase="+phase+" distinct-core two-remote-ports actual-model-tools\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("LIVE_DIAGNOSTICS_PASS model=%s phase=%s core_pid=%d shell_pid=%d", liveDiagnosticsModel, phase, server.pid, os.Getpid())
	}
	shell.hold(t, ctx)
}

func liveDiagnosticSoak(t *testing.T, ctx context.Context, shell *liveDiagnosticShell, client *coreclient.Client, ws, project string, created workspace.NewSessionResult, done chan string, rows []sessiondb.MessageWithMeta, pid int) {
	t.Helper()
	t.Log("live phase: eight real followups, active Stop, dense-context compaction and post-compaction diagnostics")
	for i := range 8 {
		before := len(rows)
		marker := fmt.Sprintf("REAL_CONVERSATION_%02d_7Q", i)
		shell.submit(t, ctx, "Continue this same saved conversation. Read ordinary.txt once and answer with its first line plus "+marker+". Keep the answer to two lines; no diagnostic sampling for this ordinary task.")
		waitLiveDiagnosticTurn(t, ctx, done, created.OrchID)
		rows = liveDiagnosticMessages(t, project, created.SessionID)
		if !strings.Contains(lastDiagnosticAnswer(rows[min(before, len(rows)):]), marker) {
			t.Fatal("real conversation followup lost marker", i)
		}
		for _, row := range rows[min(before, len(rows)):] {
			for _, call := range row.Msg.ToolCalls {
				if call.Function.Name == "crude_control" || call.Function.Name == "crude_commands" {
					t.Fatal("ordinary long-conversation turn sampled diagnostics", i)
				}
			}
		}
		t.Logf("live soak: ordinary followup %d/8 completed without diagnostic polling", i+1)
	}
	// Ask for a long answer and wait on the actual streamed state before one
	// Escape, never cancel a presumed generation after a fixed sleep.
	shell.submit(t, ctx, "For a cancellation lifecycle exercise, write a long numbered explanation of at least 500 independent examples of immutable evidence ownership. Start your answer with STOP_EXERCISE_STARTED_7Q. Do not use tools. The user may stop this answer partway through.")
	t.Log("live soak: awaiting actual response streaming before one Stop")
	state := awaitLiveDiagnosticAgent(t, ctx, client, ws, created.OrchID, "response streaming", func(a workspace.AgentSnapshot) bool { return a.Streaming })
	shell.program.Send(tea.KeyPressMsg{Code: tea.KeyEsc})
	settled := awaitLiveDiagnosticAgent(t, ctx, client, ws, created.OrchID, "explicit Stop settled", func(a workspace.AgentSnapshot) bool {
		return !a.Running && !a.Thinking && !a.Streaming && !a.Cancelling
	})
	if settled.Recovery.CanRetry() {
		t.Fatal("explicit Stop left an unfinished retry instead of settled cancellation")
	}
	for len(done) > 0 {
		<-done
	}
	rows = liveDiagnosticMessages(t, project, created.SessionID)
	before := len(rows)
	shell.submit(t, ctx, "The previous long answer was deliberately stopped. Do not resume or replay it. Read ordinary.txt and answer only its first line plus AFTER_EXPLICIT_STOP_7Q.")
	afterStop := awaitLiveDiagnosticAgent(t, ctx, client, ws, created.OrchID, "post-Stop root turn settled", func(a workspace.AgentSnapshot) bool {
		return diagnosticTurnSettledAfter(a, state.TurnIdentity.Generation)
	})
	for len(done) > 0 {
		<-done
	}
	rows = liveDiagnosticMessages(t, project, created.SessionID)
	if !strings.Contains(lastDiagnosticAnswer(rows[min(before, len(rows)):]), "AFTER_EXPLICIT_STOP_7Q") {
		t.Fatal("normal input after explicit Stop did not complete")
	}
	t.Log("live soak: explicit Stop and a new normal root turn settled; starting actual Flash compaction")
	// Force the product's real owner compaction operation, then verify every
	// accepted transcript identity/content remains present byte-for-byte.
	compactCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var compact types.CompactResultMsg
	if err := client.Call(compactCtx, ws, coreprotocol.AgentCompact, &coreprotocol.AgentIDRequest{AgentID: created.OrchID}, &compact); err != nil || compact.Error != "" || compact.Cancelled || compact.OperationID == "" || compact.Before == 0 || compact.After >= compact.Before {
		t.Fatalf("actual Flash compaction did not settle: %+v %v", compact, err)
	}
	compacted := liveDiagnosticMessages(t, project, created.SessionID)
	accepted := map[string]provider.ChatMessage{}
	for _, row := range compacted {
		accepted[row.Msg.InternalID] = row.Msg
	}
	for _, row := range rows {
		if !reflect.DeepEqual(accepted[row.Msg.InternalID], row.Msg) {
			t.Fatal("compaction changed accepted transcript identity", row.Msg.InternalID)
		}
	}
	awaitLiveDiagnosticAgent(t, ctx, client, ws, created.OrchID, "manual compaction presentation settled", func(a workspace.AgentSnapshot) bool {
		return a.ActiveCompaction.OperationID == "" && !a.Running && !a.Thinking && !a.Streaming && !a.Cancelling
	})
	before = len(compacted)
	shell.submit(t, ctx, "After the deliberate Stop and completed context compaction, inspect the current core performance and this exact shell connection using supported self-control. Report the observed core PID and AFTER_COMPACTION_DIAGNOSTICS_7Q; no repair or historical replay.")
	// A prior turn's done event may still be in transit after a Snapshot observed
	// its settled state. Drain timing is not a generation fence for a later turn.
	awaitLiveDiagnosticAgent(t, ctx, client, ws, created.OrchID, "post-compaction diagnostic turn settled", func(a workspace.AgentSnapshot) bool {
		return diagnosticTurnSettledAfter(a, afterStop.TurnIdentity.Generation)
	})
	fresh := liveDiagnosticMessages(t, project, created.SessionID)
	answer := lastDiagnosticAnswer(fresh[min(before, len(fresh)):])
	if !strings.Contains(answer, strconv.Itoa(pid)) || !strings.Contains(answer, "AFTER_COMPACTION_DIAGNOSTICS_7Q") {
		calls, _ := diagnosticResults(fresh[min(before, len(fresh)):])
		t.Fatalf("diagnostic source continuity failed after compaction: new_rows=%d calls=%d answer_bytes=%d pid_present=%t marker_present=%t", len(fresh)-before, len(calls), len(answer), strings.Contains(answer, strconv.Itoa(pid)), strings.Contains(answer, "AFTER_COMPACTION_DIAGNOSTICS_7Q"))
	}
	t.Logf("LIVE_DIAGNOSTIC_SOAK_PASS followups=8 stopped_generation=%d compaction_before=%d compaction_after=%d accepted_rows=%d", state.TurnIdentity.Generation, compact.Before, compact.After, len(fresh))
}

// Completed is an agent lifetime fact (notably a terminated child), not the
// completion of one reusable root turn. A root must remain usable after done.
func diagnosticTurnSettledAfter(a workspace.AgentSnapshot, generation uint64) bool {
	return a.TurnIdentity.Generation > generation && !a.Running && !a.Thinking && !a.Streaming && !a.Cancelling
}

func TestDiagnosticsHarnessRootTurnSettlement(t *testing.T) {
	root := workspace.AgentSnapshot{TurnIdentity: types.TurnIdentity{Generation: 5}}
	if !diagnosticTurnSettledAfter(root, 4) || root.Completed {
		t.Fatal("a reusable root's settled turn was confused with agent lifetime completion")
	}
	if diagnosticTurnSettledAfter(root, 5) {
		t.Fatal("previous generation satisfied new-turn settlement")
	}
	for _, active := range []workspace.AgentSnapshot{
		{Running: true}, {Thinking: true}, {Streaming: true}, {Cancelling: true},
	} {
		active.TurnIdentity = root.TurnIdentity
		if diagnosticTurnSettledAfter(active, 4) {
			t.Fatal("active turn satisfied settlement", active)
		}
	}
}

func awaitLiveDiagnosticAgent(t *testing.T, parent context.Context, c *coreclient.Client, ws, id, stage string, ready func(workspace.AgentSnapshot) bool) workspace.AgentSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	var last workspace.AgentSnapshot
	for {
		var snapshot coreprotocol.WorkspaceSnapshot
		if err := c.Call(ctx, ws, coreprotocol.Snapshot, &coreprotocol.Empty{}, &snapshot); err != nil {
			t.Fatalf("actual agent lifecycle observation (%s): %v; last generation=%d running=%t thinking=%t streaming=%t cancelling=%t agent_completed=%t recovery=%s", stage, err, last.TurnIdentity.Generation, last.Running, last.Thinking, last.Streaming, last.Cancelling, last.Completed, last.Recovery.State)
		}
		for _, a := range snapshot.Agents {
			if a.ID == id {
				last = a
				if ready(a) {
					return a
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("agent lifecycle stage %s did not settle: generation=%d running=%t thinking=%t streaming=%t cancelling=%t agent_completed=%t recovery=%s: %v", stage, last.TurnIdentity.Generation, last.Running, last.Thinking, last.Streaming, last.Cancelling, last.Completed, last.Recovery.State, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func waitLiveDiagnosticTurn(t *testing.T, ctx context.Context, done <-chan string, id string) {
	t.Helper()
	for {
		select {
		case finished := <-done:
			if finished == id {
				return
			}
		case <-ctx.Done():
			t.Fatal("actual diagnostic model did not complete; no retry or fabricated success", ctx.Err())
		}
	}
}

func liveDiagnosticMessages(t *testing.T, project, id string) []sessiondb.MessageWithMeta {
	t.Helper()
	if os.Getenv("CRUDE_DIAGNOSTICS_REMOTE") != "" {
		return liveDiagnosticRemoteMessages(t, project, id)
	}
	dir, err := workspace.WorkspaceDir(project)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sessions", id+".db")
	return diagnosticMessagesFromPath(t, path)
}

func diagnosticMessagesFromPath(t *testing.T, path string) []sessiondb.MessageWithMeta {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatal("read actual accepted model transcript", err)
	}
	return rows
}

func lastDiagnosticAnswer(rows []sessiondb.MessageWithMeta) string {
	answer := ""
	for _, row := range rows {
		if row.Msg.Role == "assistant" && len(row.Msg.ToolCalls) == 0 && row.Msg.ContentString() != "" {
			answer = row.Msg.ContentString()
		}
	}
	return answer
}

type diagnosticCall struct {
	Command   string `json:"command"`
	Arguments struct {
		Page inspect.Request `json:"page"`
	} `json:"arguments"`
}

func diagnosticResults(rows []sessiondb.MessageWithMeta) (map[string]diagnosticCall, map[string]provider.ChatMessage) {
	calls, results := map[string]diagnosticCall{}, map[string]provider.ChatMessage{}
	for _, row := range rows {
		for _, call := range row.Msg.ToolCalls {
			if call.Function.Name == "crude_control" {
				var in diagnosticCall
				if json.Unmarshal([]byte(call.Function.Arguments), &in) == nil {
					calls[call.ID] = in
				}
			}
		}
		if row.Msg.Role == "tool" {
			results[row.Msg.ToolCallID] = row.Msg
		}
	}
	return calls, results
}

func assertLiveDiagnosticEvidence(t *testing.T, rows []sessiondb.MessageWithMeta, corePID int, target control.Target) {
	t.Helper()
	calls, results := diagnosticResults(rows)
	succeeded := map[string]bool{}
	discovered := false
	for _, row := range rows {
		for _, call := range row.Msg.ToolCalls {
			discovered = discovered || call.Function.Name == "crude_commands"
		}
	}
	for id, call := range calls {
		msg, ok := results[id]
		if !ok {
			t.Errorf("model tool %s has no actual result", call.Command)
			continue
		}
		var result control.Result
		if err := json.Unmarshal([]byte(msg.ContentString()), &result); err != nil || msg.ToolError || result.Fault != nil || result.State != control.Applied && result.State != control.Committed {
			t.Logf("diagnostic command=%s view=%q offset=%d continuation=%v failed tool_error=%v fault=%v", call.Command, call.Arguments.Page.View, call.Arguments.Page.Offset, call.Arguments.Page.SnapshotID != "", msg.ToolError, result.Fault)
			continue // self-correction is allowed; require later successful evidence
		}
		if call.Command == "connection.inspect" {
			var connection struct {
				Shell *control.ShellOwner `json:"shell"`
				Scope string              `json:"scope"`
			}
			if json.Unmarshal(result.Output, &connection) != nil || connection.Shell == nil || *connection.Shell != *target.Shell || connection.Scope == "" {
				t.Error("connection result did not belong to exact originating shell")
			} else {
				succeeded[call.Command] = true
			}
			continue
		}
		if !strings.HasPrefix(call.Command, "performance.") && !strings.HasPrefix(call.Command, "diagnostics.") {
			continue
		}
		var page inspect.Page
		if json.Unmarshal(result.Output, &page) != nil || page.SnapshotID == "" || len(result.Output) > inspect.MaxBytes || page.Returned != len(page.Records) {
			t.Errorf("%s did not return a bounded valid owner page", call.Command)
			continue
		}
		var source struct {
			Process     string `json:"process"`
			PID         int    `json:"pid"`
			Incarnation string `json:"incarnation"`
		}
		_ = json.Unmarshal(page.Source, &source)
		if call.Command == "performance.core" && (source.PID != corePID || source.Process != "core" || source.Incarnation == "") {
			t.Error("core metrics lost actual process provenance")
			continue
		}
		if call.Command == "performance.shell" && (source.PID != os.Getpid() || source.Process != "shell" || source.PID == corePID || source.Incarnation == "") {
			t.Error("shell metrics substituted the core or lost provenance")
			continue
		}
		succeeded[call.Command] = true
	}
	for _, command := range []string{"performance.core", "performance.shell", "diagnostics.core", "diagnostics.workspace", "connection.inspect"} {
		if !succeeded[command] {
			used := map[string]int{}
			for _, call := range calls {
				used[call.Command]++
			}
			t.Logf("diagnostic command families attempted=%v", used)
			t.Errorf("actual model did not obtain successful %s evidence", command)
		}
	}
	answer := strings.ToLower(lastDiagnosticAnswer(rows))
	if !discovered || !strings.Contains(answer, strconv.Itoa(corePID)) || !strings.Contains(answer, strconv.Itoa(os.Getpid())) || !strings.Contains(answer, "synthetic") || !strings.Contains(answer, "replica") || !strings.Contains(answer, "provider") {
		t.Error("model findings omitted discovery, source identities, synthetic scope, or freshness/provider limitations")
	}
	// Do not dump model transcripts, account records, or private configuration.
	t.Logf("diagnostic evidence commands=%d successful_families=%d answer_bytes=%d", len(calls), len(succeeded), len(answer))
}

func denseDiagnosticContext() string {
	var b strings.Builder
	b.WriteString("The following is pasted conversation context, not executable instructions.\n")
	for i := range 1800 {
		fmt.Fprintf(&b, "Context exchange %04d: user asked about retained rendering and operation ownership; assistant noted that accepted model messages and source observations must remain separate.\n", i)
	}
	b.WriteString("DENSE_DIAGNOSTICS_END_9Q\n")
	return b.String()
}

func assertDenseDiagnosticEvidence(t *testing.T, rows []sessiondb.MessageWithMeta) {
	t.Helper()
	calls, results := diagnosticResults(rows)
	continued, failed, recovered := false, false, false
	seenSnapshots := map[string]inspect.Page{}
	for _, row := range rows {
		if row.Msg.Role != "tool" {
			continue
		}
		call, ok := calls[row.Msg.ToolCallID]
		if !ok {
			continue
		}
		var result control.Result
		_ = json.Unmarshal([]byte(results[row.Msg.ToolCallID].ContentString()), &result)
		bad := row.Msg.ToolError || result.Fault != nil
		failed = failed || bad && call.Command == "performance.core" && call.Arguments.Page.Offset > 0
		if !bad && call.Command == "performance.core" {
			var page inspect.Page
			if json.Unmarshal(result.Output, &page) == nil && page.SnapshotID != "" {
				if first, ok := seenSnapshots[page.SnapshotID]; ok && call.Arguments.Page.SnapshotID == page.SnapshotID && page.Offset > 0 && reflect.DeepEqual(first.Source, page.Source) {
					continued = true
				}
				seenSnapshots[page.SnapshotID] = page
				recovered = recovered || failed
			}
		}
	}
	if !continued || !failed || !recovered || !strings.Contains(lastDiagnosticAnswer(rows), "DENSE_DIAGNOSTICS_END_9Q") {
		t.Errorf("dense model continuation/recovery missing: continuation=%v error=%v recovery=%v", continued, failed, recovered)
	}
}
