package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/collaboration"
	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	"crude/types"
	"crude/workspace"
)

const liveWaitModel = "deepseek/deepseek-v4.1-flash"
const liveWaitBlocker = "WAIT_BLOCKER_7Q"
const liveWaitRelease = "WAIT_RELEASE_9R"

// Run explicitly with CRUDE_LIVE_AGENT_WAIT=1 and CRUDE_WAIT_CONFIG=/absolute/path.
// CRUDE_WAIT_ACCOUNT optionally selects an account, otherwise the configured main
// account is used. Both agents use liveWaitModel, never a mock provider. This
// creates its own App/core from the current checkout: it cannot dial a user core.
//
// Optional CRUDE_VISUAL_WAIT=1 connects a real tui.Start to that same private
// core. The caller owns sequential scripts/tuishot captures. CRUDE_WAIT_RESULT
// receives only a sanitized success marker; CRUDE_WAIT_VISUAL_HOLD_SECONDS is
// bounded to 120 seconds. No transcript, credentials or source config is copied
// to the result marker or logged.
func TestLiveAgentWaitDirectedEventJourney(t *testing.T) {
	if os.Getenv("CRUDE_LIVE_AGENT_WAIT") != "1" {
		t.Skip("set CRUDE_LIVE_AGENT_WAIT=1 and CRUDE_WAIT_CONFIG")
	}
	fc := liveWaitConfig(t)
	if _, err := exec.LookPath("curl"); err != nil {
		t.Fatal("live wait journey requires curl")
	}
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("CRUDE_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// The child only needs two loopback commands; do not pass even the path to
	// the original credential-bearing file into its subprocess environment.
	t.Setenv("CRUDE_WAIT_CONFIG", "")
	t.Setenv("CRUDE_WAIT_ACCOUNT", "")
	application, err := New(Opts{FC: fc, Home: home, ProjectDir: project})
	if err != nil {
		t.Fatal("cannot create isolated live wait application")
	}
	var ws *workspace.Workspace
	runtime, err := core.New(func(root string) (*workspace.Workspace, error) {
		var err error
		ws, err = application.newWorkspace(root, &appLogger{})
		return ws, err
	}, filepath.Join(home, "receipts.db"))
	if err != nil {
		t.Fatal("cannot create private live wait core")
	}
	t.Cleanup(func() { _ = runtime.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	dial := func(ctx context.Context, id string) (*coreclient.Client, error) {
		server, wire := net.Pipe()
		go func() { _ = core.ServeConnection(ctx, runtime, server) }()
		return coreclient.Dial(ctx, coreclient.SingleCarrier(wire), id)
	}
	client, err := dial(ctx, "wait-live-controller")
	if err != nil {
		t.Fatal("cannot connect private live wait core")
	}
	t.Cleanup(func() { _ = client.Close() })
	var attached coreprotocol.AttachResult
	if err := client.Call(ctx, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: project}, &attached); err != nil {
		t.Fatal("cannot attach private live wait workspace")
	}
	var created workspace.NewSessionResult
	if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.NewSession, &coreprotocol.InvocationSessionRequest{}, &created); err != nil || created.Error != "" {
		t.Fatal("cannot create private live wait session")
	}
	backend := ws.BackendSession(created.SessionID)
	backend.SetYolo(true)
	gates := newLiveWaitGates(ctx)
	defer func() { cancel(); gates.server.Close() }()
	updates := make(chan coreprotocol.WorkspaceUpdate, 64)
	var beginOnce sync.Once
	sub := client.Subscribe(ctx, attached.WorkspaceID, coreprotocol.TopicWorkspace, "", func(_ coreprotocol.TopicHeader, body []byte) error {
		var update coreprotocol.WorkspaceUpdate
		if err := coreprotocol.Decode(body, &update); err != nil {
			return err
		}
		if liveWaitExecuting(update, created.OrchID) {
			beginOnce.Do(func() { close(gates.begin) })
		}
		select {
		case updates <- update:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	defer sub.Close()
	notices, unsubscribe := ws.Collaboration.Subscribe()
	defer unsubscribe()
	finishVisual := startLiveWaitVisual(t, ctx, project, created.OrchID, dial)
	defer finishVisual()

	childInstruction := fmt.Sprintf(`Perform this bounded live communication acceptance task, not repository work.
1. Call execute_command with command "curl --noproxy '*' --max-time 125 -fsS %s/begin", timeout_seconds 130. Do not proceed until its output contains WAIT_BEGIN. If promoted to an async handle, read its retained output until terminal success using read_async_output/check_async_command; never rerun the command.
2. Send exactly one agent_send kind note addressed to your exact parent. Body: "%s: I need the parent to authorize the next phase before I can report." This is a blocker note, not a work request, and needs no request disposition.
3. Call execute_command with command "curl --noproxy '*' --max-time 125 -fsS %s/reply", timeout_seconds 130. This gate opens only after your parent actually sends %s. Do not publish or finish before the command succeeds; handle a promoted async command as above.
4. The reply gate returns a fresh WAIT_PROOF token. Publish exactly one final report addressed to your exact parent with that token in its BODY, not summary. Include shared execution/no file changes and successful gate evidence. End normally. No file, Git, config, credentials, external network or other shell access.`, gates.server.URL, liveWaitBlocker, gates.server.URL, liveWaitRelease)
	goal := fmt.Sprintf(`Exercise a real child blocker while waiting. First call agent_start, category crafter, isolation shared, policy default, model %s, description "Live wait blocker acceptance". Use this exact bounded assignment:
%s
After agent_start returns, immediately call agent_wait for its exact agent_id AND run_id, timeout_seconds 120, in a separate tool call. Do not send anything to the child beforehand. Its first gate is released only by your wait actually executing.
When the child's blocker note %s arrives, react promptly with exactly one agent_send kind note to that exact child whose body is "%s: proceed to final report." Do not wait for terminal completion before replying: the child cannot finish until this reply. Other runtime notices may end a wait; do not confuse them with the blocker.
Then use agent_wait for the exact child/run as needed, without polling. Read the exact published final artifact with artifact_read using the delivered artifact_id and positive exact revision. Report publication is NOT terminal success: separately observe that exact run's succeeded terminal outcome via agent_wait. Finish with WAIT_JOURNEY_OK, the proof token read from the artifact, and explain that the blocker arrived while the child run was nonterminal. Do not start another child or use tools other than agent_start, agent_wait, agent_send, artifact_read, reply.`, liveWaitModel, childInstruction, liveWaitBlocker, liveWaitRelease)
	submit := func(text string, revision uint64) {
		t.Helper()
		var admission coreprotocol.InputResult
		if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.SubmitInput, &coreprotocol.InputRequest{AgentID: created.OrchID, Text: text, DraftRevision: revision}, &admission); err != nil ||
			admission.Status != session.InputAdmissionAccepted || admission.Error != "" {
			t.Fatal("private live wait input was not admitted")
		}
	}
	submit(goal, 1)

	var events []collaboration.Event
	var cursor int64
	var blockerSender string
	var replyOnce sync.Once
	readJournal := func() {
		t.Helper()
		for {
			page, err := ws.Collaboration.Page(ctx, collaboration.PageQuery{Since: cursor, Limit: collaboration.MaxPageLimit})
			if err != nil {
				t.Fatal("cannot observe live wait journal")
			}
			for _, event := range page.Events {
				events = append(events, event)
				if event.Kind == collaboration.EventPeerNote && event.Origin == collaboration.OriginPeer &&
					strings.Contains(event.Body, liveWaitBlocker) && len(event.Recipients) == 1 && event.Recipients[0].ID == created.OrchID {
					blockerSender = event.SenderID
				}
				if liveWaitIsReply(event, created.OrchID, blockerSender) {
					replyOnce.Do(func() { close(gates.reply) })
				}
			}
			cursor = page.Cursor
			if !page.HasMore {
				return
			}
		}
	}
	awaitRoot := func() {
		t.Helper()
		for {
			select {
			case <-ctx.Done():
				counts := make(map[string]int)
				for _, call := range liveWaitCalls(backend.MessagesWithMeta()) {
					counts[call.Name]++
					if call.Name == "agent_wait" && call.Result != "" {
						var outcome struct {
							Status string `json:"status"`
						}
						if json.Unmarshal([]byte(call.Result), &outcome) == nil {
							t.Logf("wait outcome=%s", outcome.Status)
						}
					}
				}
				t.Logf("parent tool counts=%v", counts)
				for _, event := range events {
					t.Logf("journal sequence=%d kind=%s sender=%s run=%s", event.Sequence, event.Kind, event.SenderID, event.RunID)
				}
				if state, ok := ws.AgentStateSnapshot(created.OrchID); ok {
					t.Logf("parent status=%s recovery=%s", state.Status, state.Recovery.Summary)
				}
				t.Fatalf("live wait journey timed out (journal_events=%d, begin_gate=%t, reply_gate=%t)", len(events), liveWaitClosed(gates.begin), liveWaitClosed(gates.reply))
			case <-notices:
				// Read-only journal observation; never ReservePending, Admit,
				// PublishContext or write a checkpoint from this harness.
				readJournal()
			case update := <-updates:
				for _, reduction := range update.Reductions {
					if reduction.AgentID == created.OrchID && reduction.Effects.Done != nil {
						readJournal()
						return
					}
				}
			}
		}
	}
	awaitRoot()
	artifact, child, terminal := verifyLiveWaitJourney(t, ctx, ws, backend, created.OrchID, events, gates)
	requireLiveWaitVisible(t, ctx, client, attached.WorkspaceID, created.OrchID, child.ID, events)
	if final := liveLastAnswer(backend); !strings.Contains(final, "WAIT_JOURNEY_OK") || !strings.Contains(final, gates.proof) {
		t.Fatal("parent did not complete using the exact child's artifact evidence")
	}
	// A subsequent ordinary human turn must remain usable, without another
	// child or re-admission of the earlier blocker/report/outcome.
	beforeCalls := liveWaitCalls(backend.MessagesWithMeta())
	submit("Ordinary follow-up: answer exactly WAIT_FOLLOWUP_OK 42. Do not call any tools, relaunch a child, or continue the completed assignment.", 2)
	awaitRoot()
	if final := liveLastAnswer(backend); !strings.Contains(final, "WAIT_FOLLOWUP_OK 42") {
		t.Fatal("ordinary follow-up did not complete")
	}
	afterCalls := liveWaitCalls(backend.MessagesWithMeta())
	if len(beforeCalls) != len(afterCalls) {
		t.Fatal("ordinary follow-up unexpectedly executed tools")
	}
	// Re-check durable exactly-once admissions after the next normal turn.
	for _, event := range events {
		if event.SenderID == child.ID && (strings.Contains(event.Body, liveWaitBlocker) || event.ID == terminal.ID || event.Kind == collaboration.EventReportPublished) {
			requireLiveWaitAdmission(t, ws, backend.DB(), created.SessionID, created.SessionID, created.OrchID, event)
		}
	}
	requireLiveWaitVisible(t, ctx, client, attached.WorkspaceID, created.OrchID, child.ID, events)
	t.Logf("LIVE_WAIT_PASS model=%s parent=%s child=%s run=%s artifact=%s revision=%d blocker=directed_event nonterminal=true report_and_terminal=separate followup=ok",
		liveWaitModel, created.OrchID, child.ID, artifact.RunID, artifact.ID, artifact.Revision)
	if path := os.Getenv("CRUDE_WAIT_RESULT"); path != "" {
		marker := fmt.Sprintf("WAIT_SUCCESS model=%s parent=%s child=%s run=%s artifact=%s revision=%d directed_event nonterminal exactly_once report_and_terminal_separate followup_ok\n",
			liveWaitModel, created.OrchID, child.ID, artifact.RunID, artifact.ID, artifact.Revision)
		if err := os.WriteFile(path, []byte(marker), 0600); err != nil {
			t.Fatal("cannot write sanitized live wait result marker")
		}
	}
}

// Tool entry may accompany changed agent metadata, or be a conversation-only
// delta. Observe both published shapes, not a second runtime event consumer.
func liveWaitExecuting(update coreprotocol.WorkspaceUpdate, parent string) bool {
	executing := func(rows []workspace.Message) bool {
		for _, row := range rows {
			if row.ToolName == "agent_wait" && row.ToolStatus == types.ToolStatusCalling {
				return true
			}
		}
		return false
	}
	for _, change := range update.Changes.Agents {
		if change.State.ID == parent && executing(change.Conversation.Upserts) {
			return true
		}
	}
	for _, conversation := range update.Conversations {
		if conversation.AgentID == parent && executing(conversation.Delta.Upserts) {
			return true
		}
	}
	return false
}

func TestLiveAgentWaitExecutionGateObservesBothPublicationShapes(t *testing.T) {
	for _, metadata := range []bool{false, true} {
		for _, status := range []string{types.ToolStatusStreaming, types.ToolStatusCalling, types.ToolStatusSuccess} {
			for _, owner := range []string{"parent", "child"} {
				update := coreprotocol.WorkspaceUpdate{}
				delta := workspace.ConversationDelta{Upserts: []workspace.Message{{ToolName: "agent_wait", ToolStatus: status}}}
				if metadata {
					update.Changes.Agents = []coreprotocol.AgentDelta{{State: workspace.AgentSnapshot{ID: owner}, Conversation: delta}}
				} else {
					update.Conversations = []coreprotocol.ConversationUpdate{{AgentID: owner, Delta: delta}}
				}
				if got, want := liveWaitExecuting(update, "parent"), owner == "parent" && status == types.ToolStatusCalling; got != want {
					t.Fatalf("metadata=%t status=%s owner=%s executing=%t want=%t", metadata, status, owner, got, want)
				}
			}
		}
	}
}

func liveWaitConfig(t *testing.T) FileConfig {
	t.Helper()
	path := os.Getenv("CRUDE_WAIT_CONFIG")
	if path == "" || !filepath.IsAbs(path) {
		t.Fatal("CRUDE_WAIT_CONFIG must explicitly name an absolute configuration path")
	}
	source, err := loadFileConfigFrom(path)
	if err != nil {
		t.Fatal("cannot read selected live wait configuration")
	}
	selected := FileConfigToSession(source).GetActiveDefaultCategory().AccountID
	if id := os.Getenv("CRUDE_WAIT_ACCOUNT"); id != "" {
		selected = id
	}
	account, ok := source.Accounts[selected]
	if !ok || account.ID != selected || account.APIKey == "" || account.OAuth != nil {
		t.Fatal("selected live wait account must have an API key and no refreshable OAuth credentials")
	}
	on, off := true, false
	main := session.CategoryConfig{
		Enabled: &on, AccountID: selected, Model: liveWaitModel,
		ThinkingEffort: "low", Policy: "default", ModelSupportsTools: true,
		AllowedTools: []string{"agent_start", "agent_wait", "agent_send", "artifact_read", "reply"},
	}
	child := main
	child.AllowedTools = []string{"execute_command", "read_async_output", "check_async_command", "agent_send", "publish_report", "artifact_read"}
	agents := session.AgentsConfig{
		Enabled: true, ActiveProfile: "wait-acceptance", MaxConcurrent: 1,
		Profiles: map[string]session.ProfileConfig{"wait-acceptance": {
			MultiAgent: &on, TurnLogs: &off,
			Categories: map[string]session.CategoryConfig{session.CategoryDefault: main, session.CategoryCrafter: child},
		}},
	}
	memory := types.MemorySettings{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"}
	// Deliberately rebuild, rather than retaining source.raw, unrelated accounts,
	// profiles, skills, MCP registrations or credential-bearing log settings.
	return FileConfig{
		Accounts: map[string]provider.Account{selected: provider.CloneAccount(account)},
		Agents:   &agents, Memory: &memory, TurnLogsEnabled: &off, Network: source.Network,
	}
}

type liveWaitGates struct {
	server *httptest.Server
	begin  chan struct{}
	reply  chan struct{}
	proof  string
}

func newLiveWaitGates(ctx context.Context) *liveWaitGates {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		panic("cannot create live wait proof nonce")
	}
	g := &liveWaitGates{begin: make(chan struct{}), reply: make(chan struct{}), proof: "WAIT_PROOF_" + hex.EncodeToString(nonce[:])}
	g.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ready <-chan struct{}
		var text string
		switch r.URL.Path {
		case "/begin":
			ready, text = g.begin, "WAIT_BEGIN"
		case "/reply":
			ready, text = g.reply, g.proof
		default:
			http.NotFound(w, r)
			return
		}
		timer := time.NewTimer(120 * time.Second)
		defer timer.Stop()
		select {
		case <-ready:
			_, _ = fmt.Fprintln(w, text)
		case <-ctx.Done():
			http.Error(w, "journey ended", http.StatusRequestTimeout)
		case <-r.Context().Done():
		case <-timer.C:
			http.Error(w, "gate timed out", http.StatusGatewayTimeout)
		}
	}))
	return g
}

func liveWaitClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func liveWaitIsReply(event collaboration.Event, parent, child string) bool {
	return event.SenderID == parent && event.Origin == collaboration.OriginPeer &&
		event.Kind == collaboration.EventPeerNote && strings.Contains(event.Body, liveWaitRelease) &&
		child != "" && len(event.Recipients) == 1 && event.Recipients[0].ID == child
}

type liveWaitCall struct {
	Name, Args, Result string
}

func liveWaitCalls(rows []sessiondb.MessageWithMeta) map[string]liveWaitCall {
	calls := make(map[string]liveWaitCall)
	for _, row := range rows {
		for _, call := range row.Msg.ToolCalls {
			calls[call.ID] = liveWaitCall{Name: call.Function.Name, Args: call.Function.Arguments}
		}
		if row.Msg.Role == "tool" {
			call := calls[row.Msg.ToolCallID]
			call.Result = row.Msg.ContentString()
			calls[row.Msg.ToolCallID] = call
		}
	}
	return calls
}

func verifyLiveWaitJourney(t *testing.T, ctx context.Context, ws *workspace.Workspace, backend *session.Session, parent string, events []collaboration.Event, gates *liveWaitGates) (collaboration.Artifact, collaboration.Agent, collaboration.Event) {
	t.Helper()
	if !liveWaitClosed(gates.begin) || !liveWaitClosed(gates.reply) {
		t.Fatal("journey bypassed actual wait or parent reply gate")
	}
	var blocker, reply, report, terminal collaboration.Event
	for _, event := range events {
		if event.SenderID != parent && event.Kind == collaboration.EventPeerNote && strings.Contains(event.Body, liveWaitBlocker) {
			if blocker.ID != "" {
				t.Fatal("duplicate child blocker publication")
			}
			blocker = event
		}
		if liveWaitIsReply(event, parent, blocker.SenderID) {
			if reply.ID != "" {
				t.Fatal("duplicate parent release publication")
			}
			reply = event
		}
	}
	if blocker.ID == "" || reply.ID == "" {
		t.Fatal("real directed blocker/reply events are missing")
	}
	child, err := ws.Collaboration.Agent(ctx, blocker.SenderID)
	if err != nil || child.ParentID != parent || len(blocker.Recipients) != 1 || blocker.Recipients[0].ID != parent ||
		len(reply.Recipients) != 1 || reply.Recipients[0].ID != child.ID {
		t.Fatal("blocker/reply sender or recipient identity is wrong")
	}
	for _, event := range events {
		if event.SenderID == child.ID && event.Kind == collaboration.EventReportPublished {
			if report.ID != "" {
				t.Fatal("child published more than one report")
			}
			report = event
		}
		if event.SenderID == child.ID && event.Kind == collaboration.EventRunOutcome {
			terminal = event
		}
	}
	if report.ID == "" || terminal.ID == "" || report.ID == terminal.ID ||
		blocker.Sequence >= reply.Sequence || reply.Sequence >= report.Sequence || report.Sequence >= terminal.Sequence {
		t.Fatal("blocker, reaction, report publication and terminal outcome were not separate ordered events")
	}
	var artifact collaboration.Artifact
	for _, ref := range report.References {
		if ref.Kind == "report" {
			artifact, err = ws.Collaboration.Artifact(ctx, ref.ID, ref.Revision)
			if err != nil {
				t.Fatal("cannot read exact child final report")
			}
		}
	}
	if artifact.ID == "" || artifact.AuthorID != child.ID || artifact.Kind != collaboration.ReportFinal ||
		artifact.RunID != terminal.RunID || !strings.Contains(artifact.Body, gates.proof) {
		t.Fatal("child final artifact lacks exact author/run/proof evidence")
	}
	run, err := ws.Collaboration.Run(ctx, artifact.RunID)
	if err != nil || run.State != collaboration.RunSucceeded {
		t.Fatal("published report did not have a separate succeeded terminal run")
	}
	counts := map[string]int{}
	var woke, readExact, observedTerminal bool
	for _, call := range liveWaitCalls(backend.MessagesWithMeta()) {
		counts[call.Name]++
		switch call.Name {
		case "agent_wait":
			var input struct {
				AgentID string `json:"agent_id"`
				RunID   string `json:"run_id"`
			}
			var result struct {
				Status string               `json:"status"`
				Event  *collaboration.Event `json:"event"`
				Run    *collaboration.Run   `json:"run"`
			}
			if json.Unmarshal([]byte(call.Args), &input) != nil || json.Unmarshal([]byte(call.Result), &result) != nil {
				t.Fatal("agent_wait did not retain structured tool arguments/results")
			}
			if result.Status == "directed_event" && result.Event != nil && result.Event.ID == blocker.ID {
				if input.AgentID != child.ID || input.RunID != artifact.RunID || result.Event.SenderID != child.ID ||
					result.Run == nil || result.Run.ID != artifact.RunID || result.Run.State.Terminal() {
					t.Fatal("blocker wait wake lost exact event/sender/nonterminal run")
				}
				woke = true
			}
			if input.RunID == artifact.RunID && result.Run != nil && result.Run.State == collaboration.RunSucceeded {
				observedTerminal = true
			}
		case "artifact_read":
			var input struct {
				ID       string `json:"artifact_id"`
				Revision int    `json:"revision"`
			}
			if json.Unmarshal([]byte(call.Args), &input) == nil && input.ID == artifact.ID && input.Revision == artifact.Revision && strings.Contains(call.Result, gates.proof) {
				readExact = true
			}
		}
	}
	if counts["agent_start"] != 1 || counts["agent_send"] != 1 || !woke || !readExact || !observedTerminal {
		t.Fatalf("parent bypassed live wait acceptance: starts=%d sends=%d directed_blocker=%t exact_artifact=%t terminal_observed=%t", counts["agent_start"], counts["agent_send"], woke, readExact, observedTerminal)
	}
	for _, event := range []collaboration.Event{blocker, report, terminal} {
		requireLiveWaitAdmission(t, ws, backend.DB(), backend.SessionID(), backend.SessionID(), parent, event)
	}
	childPath := filepath.Join(backend.SessionDir(), "subagents", child.SessionID+".db")
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(childPath), RawQuery: "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(1000)"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal("cannot inspect completed child receipts read-only")
	}
	defer func() { _ = db.Close() }()
	requireLiveWaitAdmission(t, ws, db, child.SessionID, child.ID, child.ID, reply)
	for _, database := range []*sql.DB{backend.DB(), db} {
		var requests int
		if err := database.QueryRow(`SELECT COUNT(*) FROM usage_requests WHERE model_id=? AND request_outcome='succeeded'`, liveWaitModel).Scan(&requests); err != nil || requests == 0 {
			t.Fatal("parent or child lacks a successful real DeepSeek provider request")
		}
		var other int
		if err := database.QueryRow(`SELECT COUNT(*) FROM usage_requests WHERE model_id<>?`, liveWaitModel).Scan(&other); err != nil || other != 0 {
			t.Fatal("journey used an unexpected model")
		}
	}
	return artifact, child, terminal
}

func requireLiveWaitAdmission(t *testing.T, ws *workspace.Workspace, db *sql.DB, sessionID, contextOwner, recipient string, event collaboration.Event) {
	t.Helper()
	var delivery, state, commit string
	var version, epoch uint64
	if err := ws.WorkspaceDB.DB.QueryRow(`SELECT id,state,version,reserved_epoch,context_commit_id FROM collaboration_deliveries WHERE event_id=? AND recipient_id=?`,
		event.ID, recipient).Scan(&delivery, &state, &version, &epoch, &commit); err != nil || state != "admitted" || version != 3 || commit == "" {
		t.Fatalf("event %s lacks exactly one ordinary delivery admission", event.ID)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM collaboration_admissions WHERE delivery_id=? AND event_id=? AND session_id=? AND agent_id=? AND runtime_epoch=? AND delivery_version=2 AND context_commit_id=? AND published_at>0`,
		delivery, event.ID, sessionID, recipient, epoch, commit).Scan(&count); err != nil || count != 1 {
		t.Fatalf("event %s lacks exact local commit/workspace receipt agreement", event.ID)
	}
	contextState, found, err := sessiondb.LoadContextState(db, sessionID, contextOwner)
	if err != nil || !found {
		t.Fatal("cannot read normally published live agent context")
	}
	count = 0
	for _, message := range contextState.Messages {
		if message.InternalID == event.ID {
			count++
			if message.Collaboration == nil || message.Collaboration.EventID != event.ID || message.Collaboration.SenderID != event.SenderID {
				t.Fatal("context event lost sender/identity attribution")
			}
		}
	}
	if count != 1 {
		t.Fatalf("event %s appears %d times in ordinary context", event.ID, count)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM immutable_message_identities WHERE internal_id=? AND session_id=? AND agent_id=?`,
		event.ID, sessionID, contextOwner).Scan(&count); err != nil || count != 1 {
		t.Fatalf("event %s lacks its single durable context identity: count=%d err=%v", event.ID, count, err)
	}
	// Admission can retain a peer event as context-only; normal finalization
	// may also persist its visible projection. Neither path may duplicate it.
	// The full shell snapshot below independently requires one visible row.
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE internal_id=?`, event.ID).Scan(&count); err != nil || count > 1 {
		t.Fatalf("event %s duplicated its transcript projection: count=%d err=%v", event.ID, count, err)
	}
}

func requireLiveWaitVisible(t *testing.T, ctx context.Context, client *coreclient.Client, workspaceID, recipient, child string, events []collaboration.Event) {
	t.Helper()
	// AgentStateSnapshot intentionally omits transcript bodies. Use the actual
	// framed full snapshot that a newly attached shell receives instead.
	var snapshot coreprotocol.WorkspaceSnapshot
	if err := client.Call(ctx, workspaceID, coreprotocol.Snapshot, &coreprotocol.Empty{}, &snapshot); err != nil {
		t.Fatal("cannot read full shell conversation snapshot")
	}
	var messages []workspace.Message
	for _, state := range snapshot.Agents {
		if state.ID == recipient {
			messages = state.Messages
		}
	}
	for _, event := range events {
		if event.SenderID != child {
			continue
		}
		if !strings.Contains(event.Body, liveWaitBlocker) && event.Kind != collaboration.EventReportPublished && event.Kind != collaboration.EventRunOutcome {
			continue
		}
		count := 0
		for _, message := range messages {
			if message.Collaboration != nil && message.Collaboration.EventID == event.ID {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("event %s appears %d times in the full shell conversation", event.ID, count)
		}
	}
}

func TestLiveAgentWaitGateCancellationAndReplyIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	gates := newLiveWaitGates(ctx)
	defer gates.server.Close()
	defer cancel()
	result := make(chan int, 1)
	go func() {
		client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
		response, err := client.Get(gates.server.URL + "/begin")
		if err != nil {
			result <- 0
			return
		}
		defer func() { _ = response.Body.Close() }()
		result <- response.StatusCode
	}()
	cancel()
	select {
	case status := <-result:
		if status != http.StatusRequestTimeout {
			t.Fatalf("cancelled gate status=%d", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled journey retained a blocked gate")
	}
	event := collaboration.Event{SenderID: "parent", Kind: collaboration.EventPeerNote, Origin: collaboration.OriginPeer, Body: liveWaitRelease, Recipients: []collaboration.Recipient{{ID: "child"}}}
	if !liveWaitIsReply(event, "parent", "child") {
		t.Fatal("real directed parent note did not match reply gate")
	}
	event.SenderID = "unrelated"
	if liveWaitIsReply(event, "parent", "child") {
		t.Fatal("unrelated sender opened reply gate")
	}
	event.SenderID, event.Kind = "parent", collaboration.EventRunOutcome
	if liveWaitIsReply(event, "parent", "child") {
		t.Fatal("terminal outcome opened reply gate")
	}
	event.Kind = collaboration.EventPeerNote
	event.Recipients[0].ID = "other-child"
	if liveWaitIsReply(event, "parent", "child") {
		t.Fatal("reply addressed to another child opened reply gate")
	}
}

func TestLiveAgentWaitConfigRestrictsAccountAndModel(t *testing.T) {
	on := true
	source := FileConfig{
		Accounts: map[string]provider.Account{
			"selected": {ID: "selected", ProviderID: "custom", APIKey: "synthetic-test-key"},
			"unused":   {ID: "unused", ProviderID: "custom", APIKey: "synthetic-unused-key"},
		},
		TurnLogsEnabled: &on,
		Agents: &session.AgentsConfig{ActiveProfile: "source", Profiles: map[string]session.ProfileConfig{
			"source": {Categories: map[string]session.CategoryConfig{
				session.CategoryDefault: {AccountID: "unused", Model: "not-the-acceptance-model"},
			}},
		}},
	}
	path := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CRUDE_WAIT_CONFIG", path)
	t.Setenv("CRUDE_WAIT_ACCOUNT", "selected")
	filtered := liveWaitConfig(t)
	if len(filtered.Accounts) != 1 || filtered.Accounts["selected"].APIKey != "synthetic-test-key" ||
		len(filtered.raw) != 0 || filtered.IsTurnLogsEnabled() || len(filtered.Agents.Profiles) != 1 {
		t.Fatal("live wait config retained unrelated accounts, raw source or turn logs")
	}
	cfg := FileConfigToSession(filtered)
	for _, category := range []string{session.CategoryDefault, session.CategoryCrafter} {
		got := cfg.GetCategoryConfig(category)
		if got.AccountID != "selected" || got.Model != liveWaitModel || got.Inherit != "" {
			t.Fatal("live parent/child connection was not fixed to selected account and exact model")
		}
	}
	if cfg.Memory.AutoRecall != "off" || cfg.Memory.AutoExtract != "off" || cfg.Memory.Consolidation != "off" {
		t.Fatal("background memory jobs were not disabled")
	}
}
