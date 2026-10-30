package remote

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
)

var stateFenceNames = [...]string{
	coreprotocol.TopicStatus, coreprotocol.TopicSettings, coreprotocol.TopicLSP,
	coreprotocol.TopicTerminalInventory, coreprotocol.TopicRegistry,
	coreprotocol.TopicGit, coreprotocol.TopicBoard,
}

// The authority is deliberately just scalar facts, not an exported replica,
// reducer, installation helper, or production sequence-admission predicate.
type stateFenceOracle struct {
	values        [7]int
	seq           [7]uint64
	clients       [7]*coreclient.Client
	statusVersion uint64
	statusChanges uint64
	statusClient  *coreclient.Client
	faults        [7]string
}

func stateFenceLabel(value int) string { return fmt.Sprintf("value-%d-界", value) }

func stateFenceValue(topic, value int, version uint64) coreprotocol.WorkspaceState {
	label := stateFenceLabel(value)
	switch topic {
	case 0:
		return coreprotocol.WorkspaceState{Status: &coreprotocol.StatusSnapshot{Version: version,
			Notifications: []types.NotificationRecord{{ID: label, Summary: label}}}}
	case 1:
		return coreprotocol.WorkspaceState{Settings: &coreprotocol.SettingsFetchResult{
			ApplicationRevision: label, WorkspaceRevision: label,
			Application: types.AppSettings{Agents: types.AgentsSettings{ActiveProfile: label}},
			Effective:   types.AppSettings{Agents: types.AgentsSettings{ActiveProfile: label}},
			Accounts: map[string]coreprotocol.AccountView{"account": {
				ID: "account", Label: label, APIKeyPresent: true, OAuthPresent: true,
				HeadersPresent: true, ExtraBodyPresent: true,
			}},
		}, AgentDefaults: &types.AgentsSettings{ActiveProfile: label, MaxConcurrent: value}}
	case 2:
		return coreprotocol.WorkspaceState{LSP: &coreprotocol.LSPSnapshot{
			Version: uint64(value), Available: true, Connected: true, Ready: true,
			Status: types.LSPConnected, ShowInEditor: true,
			Generation:  "fuzz-owner",
			Diagnostics: map[string]types.InsightDiagnostics{"/fuzz.go": {ID: label, Diagnostics: []types.LSPDiagnostic{{Message: label, Line: value, Severity: types.DiagWarning}}}},
		}}
	case 3:
		infos := coreprotocol.TerminalInfos{{ID: "terminal", Title: label, Cols: 80, Rows: 24, Alive: value%2 == 0}}
		return coreprotocol.WorkspaceState{Terminals: &infos}
	case 4:
		registry := coreprotocol.SessionRegistry{{ID: label, Model: label, MessageCount: value}}
		return coreprotocol.WorkspaceState{Registry: &registry}
	case 5:
		return coreprotocol.WorkspaceState{Git: &coreprotocol.GitSnapshotResult{Snapshot: workspace.GitSnapshot{
			Branch: label, Head: label, Ahead: value, Remotes: []string{label},
		}}}
	case 6:
		return coreprotocol.WorkspaceState{Board: &coreprotocol.BoardSnapshotResult{Snapshot: workspace.BoardSnapshot{
			WorkspaceID: label, Unread: value,
		}}}
	default:
		panic("invalid fuzz topic")
	}
}

func stateFenceDecode(t *testing.T, source coreprotocol.WorkspaceState) coreprotocol.WorkspaceState {
	t.Helper()
	body, err := coreprotocol.Marshal(&source)
	if err != nil {
		t.Fatal(err)
	}
	var decoded coreprotocol.WorkspaceState
	if err := coreprotocol.Decode(body, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func (a *stateFenceOracle) check(t *testing.T, w *Workspace) {
	t.Helper()
	if len(w.topicCursors) != 8 || len(w.topicClients) != 8 {
		t.Fatal("publication created an unexpected topic cursor/domain")
	}
	for i, name := range stateFenceNames {
		if w.topicCursors[name] != a.seq[i] || w.topicClients[name] != a.clients[i] {
			t.Fatalf("%s cursor/domain: got %d/%p want %d/%p", name, w.topicCursors[name], w.topicClients[name], a.seq[i], a.clients[i])
		}
	}
	notices := w.Status().Notifications(false)
	if len(notices) != 2 || notices[0].Summary != "private-toast" || notices[1].ID != stateFenceLabel(a.values[0]) || notices[1].Summary != stateFenceLabel(a.values[0]) || w.status.core.Version != a.statusVersion || w.Status().Version() != a.statusChanges {
		t.Fatalf("status projection/revision/private toast diverged: %+v version=%d/%d", notices, w.status.core.Version, w.Status().Version())
	}
	label := stateFenceLabel(a.values[1])
	if w.settings.current.fetch.ApplicationRevision != label || w.settings.current.fetch.WorkspaceRevision != label || w.LoadAppSettings().Agents.ActiveProfile != label || w.AppDefaults().Agents.ActiveProfile != label || w.WorkspaceAgentDefaults().ActiveProfile != label || w.WorkspaceAgentDefaults().MaxConcurrent != a.values[1] {
		t.Fatal("settings/defaults projection diverged")
	}
	// Presence flags must remain write-only markers in the actual settings read
	// model and Keep drafts; no fixture needs a real credential value.
	accounts := w.AllAccounts()
	account, found := accounts["account"]
	if len(accounts) != 1 || !found || account.Label != label || account.APIKey != StoredSecret || account.OAuth == nil || account.OAuth.AccessToken != StoredSecret || account.OAuth.RefreshToken != StoredSecret || len(account.Connection.Headers) != 1 || account.Connection.Headers[storedKey] != StoredSecret || len(account.Connection.ExtraBody) != 1 || account.Connection.ExtraBody[storedKey] != StoredSecret {
		t.Fatal("write-only settings markers diverged")
	}
	keep := draft(account, w.settings.current.fetch.Accounts["account"], true)
	if !keep.APIKey.Keep || keep.APIKey.Value != nil || !keep.OAuth.Keep || keep.OAuth.Value != nil || !keep.Headers.Keep || len(keep.Headers.Value) != 0 || !keep.ExtraBody.Keep || keep.ExtraBody.Value != nil {
		t.Fatal("unchanged write-only markers became secret writes")
	}
	diagnostics := w.LSPMgr().DiagnosticSnapshot()
	batch := diagnostics.Diagnostics["/fuzz.go"]
	ds := batch.Diagnostics
	connected, status, problem := w.LSPMgr().Status()
	if diagnostics.Generation != "fuzz-owner" || batch.ID != stateFenceLabel(a.values[2]) || diagnostics.Version != uint64(a.values[2]) || len(diagnostics.Diagnostics) != 1 || len(ds) != 1 || ds[0].Message != stateFenceLabel(a.values[2]) || ds[0].Line != a.values[2] || ds[0].Severity != types.DiagWarning || !connected || status != types.LSPConnected || problem != "" || !w.LSPMgr().ServerReady() || !w.LSPMgr().ShowDiagnosticsInEditor() || w.LSPMgr().ShowDiagnosticsInTree() {
		t.Fatal("LSP projection diverged")
	}
	infos := w.TermMgr().List()
	if len(infos) != 1 || infos[0].ID != "terminal" || infos[0].Title != stateFenceLabel(a.values[3]) || infos[0].Alive != (a.values[3]%2 == 0) || infos[0].Cols != 80 || infos[0].Rows != 24 {
		t.Fatal("terminal inventory diverged")
	}
	registry := w.ListSessions()
	if len(registry) != 1 || registry[0].ID != stateFenceLabel(a.values[4]) || registry[0].Model != stateFenceLabel(a.values[4]) || registry[0].MessageCount != a.values[4] {
		t.Fatal("registry projection diverged")
	}
	if w.gitState == nil || w.gitState.Snapshot.Branch != stateFenceLabel(a.values[5]) || w.gitState.Snapshot.Head != stateFenceLabel(a.values[5]) || w.gitState.Snapshot.Ahead != a.values[5] || len(w.gitState.Snapshot.Remotes) != 1 || w.gitState.Snapshot.Remotes[0] != stateFenceLabel(a.values[5]) {
		t.Fatal("Git projection diverged")
	}
	if w.boardState == nil || w.boardState.Snapshot.WorkspaceID != stateFenceLabel(a.values[6]) || w.boardState.Snapshot.Unread != a.values[6] {
		t.Fatal("Board projection diverged")
	}
	for _, i := range []int{5, 6} {
		got := w.gitError
		if i == 6 {
			got = w.boardError
		}
		if got == nil && a.faults[i] != "" || got != nil && got.Error() != a.faults[i] {
			t.Fatalf("%s fault diverged: %v", stateFenceNames[i], got)
		}
	}
	if w.view.Revision() != 1 || w.view.AgentMgr.Get("root").State.Messages.Messages()[0].Content != "retained-log" || w.topicCursors[coreprotocol.TopicWorkspace] != 1 {
		t.Fatal("state publication overwrote workspace log")
	}
}

// Full detached projections additionally guard atomic rejection, including
// fields not selected by the scalar success oracle. They are never used to
// calculate expected successful state.
func stateFenceProjection(t *testing.T, w *Workspace) coreprotocol.WorkspaceState {
	t.Helper()
	registry := coreprotocol.SessionRegistry(w.registry)
	terminals := coreprotocol.TerminalInfos(w.terminals.infos)
	s := coreprotocol.WorkspaceState{Status: &w.status.core, Settings: &w.settings.current.fetch,
		AgentDefaults: &w.settings.current.defaults, LSP: &w.lsp.snapshot,
		Registry: &registry, Terminals: &terminals, Git: w.gitState, Board: w.boardState}
	return stateFenceDecode(t, s)
}

func FuzzRemoteStateTopicFences(f *testing.F) {
	f.Add([]byte{})
	// Every operation has a balanced seven-topic seed; do not depend on the
	// mutator discovering a particular projection or rejection class.
	for op := 0; op < 18; op++ {
		seed := make([]byte, 7)
		for topic := range stateFenceNames {
			seed[topic] = byte(op*7 + topic)
		}
		f.Add(seed)
	}
	for topic := range stateFenceNames {
		var seed []byte
		for _, op := range []int{1, 13, 0, 12, 4, 14, 16, 0, 2, 11} {
			seed = append(seed, byte(op*7+topic))
		}
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, actions []byte) {
		if len(actions) > 32 {
			actions = actions[:32]
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		clients := [2]*coreclient.Client{{}, {}}
		view, err := coreclient.NewWorkspaceView(clients[0], coreprotocol.WorkspaceSnapshot{
			ID: "fuzz-workspace", Root: "/fuzz-project", Revision: 1,
			Agents: []workspace.AgentSnapshot{{ID: "root", ConversationRevision: 1,
				Messages: []workspace.Message{{InternalID: "message", Role: workspace.MessageRoleAssistant, Content: "retained-log"}}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		w := &Workspace{ctx: ctx, client: clients[0], view: view, initialized: true, logger: nopLogger{},
			topics: make(map[string]*replicaTopic), topicClients: map[string]*coreclient.Client{coreprotocol.TopicWorkspace: clients[0]},
			topicCursors: map[string]uint64{coreprotocol.TopicWorkspace: 1}}
		w.status, w.settings, w.lsp, w.terminals = newStatusReplica(w), &settingsReplica{w: w}, newLSPReplica(w), &terminalReplica{w: w}
		w.status.transient = []types.NotificationRecord{{ID: "private", Summary: "private-toast", Transient: true}}
		var contexts [7]context.Context
		newToken := func(i int) {
			c, stop := context.WithCancel(ctx)
			contexts[i] = c
			w.topicMu.Lock()
			if old := w.topics[stateFenceNames[i]]; old != nil {
				old.cancel()
			}
			w.topics[stateFenceNames[i]] = &replicaTopic{cancel: stop}
			w.topicMu.Unlock()
		}
		batchFor := func(i, value int, version, seq uint64) publicationBatch {
			return publicationBatch{client: w.client, ctx: contexts[i], owner: w.topics[stateFenceNames[i]],
				topic: stateFenceNames[i], header: coreprotocol.TopicHeader{Seq: seq},
				state: stateFenceDecode(t, stateFenceValue(i, value, version)), done: make(chan error, 1)}
		}
		var oracle stateFenceOracle
		ready := false
		commit := func(i int, b publicationBatch, value int, version uint64) {
			t.Helper()
			if len(b.done) != 0 {
				t.Fatal("publication acknowledged before UI application")
			}
			result := w.ApplyReplicaChanges(shellport.ReplicaChangesMsg{Owner: w, Batch: b})
			select {
			case err := <-b.done:
				if err != nil {
					t.Fatalf("valid %s seq=%d reset=%v rejected: %v", b.topic, b.header.Seq, b.header.Reset, err)
				}
			default:
				t.Fatal("UI commit did not release applied ACK")
			}
			oracle.seq[i], oracle.clients[i] = b.header.Seq, b.client
			if b.state.Fault == nil {
				if i == 0 && oracle.statusClient != nil && oracle.statusClient != b.client {
					oracle.statusVersion, oracle.statusChanges = 0, 0
				}
				if i != 0 || version > oracle.statusVersion || oracle.statusChanges == 0 {
					oracle.values[i] = value
				}
				if i == 0 && (version > oracle.statusVersion || oracle.statusChanges == 0) {
					oracle.statusVersion = version
					oracle.statusChanges++
				}
				if i == 0 {
					oracle.statusClient = b.client
				}
				oracle.faults[i] = ""
				if b.owner != nil && !b.owner.applied {
					t.Fatal("committed current token was not marked applied")
				}
			} else {
				if i == 5 || i == 6 {
					oracle.faults[i] = "fuzz: source unavailable"
				}
				if len(result.Follow) != 1 {
					t.Fatal("fault publication did not surface failure")
				}
			}
			if ready {
				oracle.check(t, w) // ACK success must imply every projection is committed.
			}
		}
		reject := func(b publicationBatch, want error) {
			t.Helper()
			before := stateFenceProjection(t, w)
			oldApplied := b.owner != nil && b.owner.applied
			out := w.ApplyReplicaChanges(shellport.ReplicaChangesMsg{Owner: w, Batch: b})
			select {
			case got := <-b.done:
				if !errors.Is(got, want) {
					t.Fatalf("%s rejection: got %v want %v", b.topic, got, want)
				}
			default:
				t.Fatal("rejected publication did not release waiter")
			}
			if !reflect.DeepEqual(before, stateFenceProjection(t, w)) || b.owner != nil && b.owner.applied != oldApplied {
				t.Fatal("rejected publication partially installed state or marked token applied")
			}
			if len(out.Notices)+len(out.Reductions)+len(out.Attached) != 0 || out.SessionsChanged || out.Initial || errors.Is(want, context.Canceled) && len(out.Follow) != 0 {
				t.Fatal("rejected publication emitted successful effects")
			}
			oracle.check(t, w)
		}
		for i := range stateFenceNames {
			newToken(i)
			commit(i, batchFor(i, i+1, 100, 100), i+1, 100)
		}
		ready = true
		oracle.check(t, w)
		// Unlike latest-state slots, the workspace log may not skip a sequence.
		reject(publicationBatch{client: w.client, ctx: ctx, topic: coreprotocol.TopicWorkspace,
			header: coreprotocol.TopicHeader{Seq: 3}, done: make(chan error, 1),
			update: coreprotocol.WorkspaceUpdate{Changes: coreprotocol.AgentChangesResult{Revision: 2}}}, coreclient.ErrTopicReset)
		for step, action := range actions {
			i, op := int(action)%7, int(action)/7%18
			value, version := 20+step, oracle.statusVersion+1
			b := batchFor(i, value, version, oracle.seq[i]+1)
			switch op {
			case 0: // ordinary latest value
				commit(i, b, value, version)
			case 1: // conflation permits sequence gaps
				b.header.Seq += 7
				commit(i, b, value, version)
			case 2, 3: // equal or older same-domain value
				if oracle.clients[i] != w.client {
					// Establish this connection's domain before testing staleness;
					// an old domain's cursor cannot make a new core's value stale.
					commit(i, b, value, version)
					b = batchFor(i, value+100, version+1, oracle.seq[i]+1)
				}
				b.header.Seq = oracle.seq[i]
				if op == 3 && b.header.Seq > 0 {
					b.header.Seq--
				}
				reject(b, coreclient.ErrTopicReset)
			case 4: // explicit reset can lower the sequence in the same domain
				b.header.Reset, b.header.Seq = true, 1
				commit(i, b, value, version)
			case 5: // actual Apply path, valid projection routed to wrong topic
				b.state = stateFenceDecode(t, stateFenceValue((i+1)%7, value, version))
				reject(b, coreclient.ErrTopicReset)
			case 6:
				b.state = stateFenceDecode(t, coreprotocol.WorkspaceState{})
				reject(b, coreclient.ErrTopicReset)
			case 7: // more than one projection must not partially install
				if i == 0 {
					b.state.LSP = &coreprotocol.LSPSnapshot{Version: 999}
				} else {
					b.state.Status = &coreprotocol.StatusSnapshot{Version: 999}
				}
				b.state = stateFenceDecode(t, b.state)
				reject(b, coreclient.ErrTopicReset)
			case 8: // settings needs defaults; other topics must not carry them
				if i == 1 {
					b.state.AgentDefaults = nil
				} else {
					b.state.AgentDefaults = &types.AgentsSettings{ActiveProfile: "must-not-install"}
				}
				b.state = stateFenceDecode(t, b.state)
				reject(b, coreclient.ErrTopicReset)
			case 9:
				canceled, stop := context.WithCancel(ctx)
				stop()
				b.ctx = canceled
				reject(b, context.Canceled)
			case 10: // disconnected queued client, even with valid body/sequence
				if b.client == clients[0] {
					b.client = clients[1]
				} else {
					b.client = clients[0]
				}
				reject(b, context.Canceled)
			case 11: // wrong shell owner ignores the message, including its waiter
				canceled, stop := context.WithCancel(ctx)
				b.ctx = canceled
				before := stateFenceProjection(t, w)
				out := w.ApplyReplicaChanges(shellport.ReplicaChangesMsg{Owner: &Workspace{}, Batch: b})
				if len(b.done) != 0 || !reflect.DeepEqual(out, shellport.ReplicaApplied{}) || !reflect.DeepEqual(before, stateFenceProjection(t, w)) {
					t.Fatal("wrong owner consumed publication or acknowledged another shell")
				}
				stop() // explicit subscription cancellation releases its own waiter
				if !errors.Is(b.ctx.Err(), context.Canceled) {
					t.Fatal("ignored owner's waiter was not explicitly canceled")
				}
				oracle.check(t, w)
			case 12: // real reset cancellation fences queued old token and context
				w.resetTopic(b.topic)
				newToken(i)
				reject(b, context.Canceled)
				current := batchFor(i, value, version, 1)
				current.header.Reset = true
				commit(i, current, value, version)
				b.done = make(chan error, 1)
				reject(b, context.Canceled) // old value may never overwrite reset success
			case 13: // new connection admits a genuinely lower revision domain
				w.connMu.Lock()
				if w.client == clients[0] {
					w.client = clients[1]
				} else {
					w.client = clients[0]
				}
				w.connMu.Unlock()
				// Fresh pointers are never recycled: a later reconnect must not make
				// a very old topic domain look current by pointer reuse.
				clients = [2]*coreclient.Client{w.client, {}}
				for j := range stateFenceNames {
					newToken(j)
				}
				reject(b, context.Canceled)
				b = batchFor(i, value, 1, 1)
				commit(i, b, value, 1)
			case 14: // status's inner revision prevents stale same-core notices
				if i == 0 {
					b.state = stateFenceDecode(t, stateFenceValue(i, value, oracle.statusVersion))
					commit(i, b, value, oracle.statusVersion)
				} else {
					commit(i, b, value, version)
				}
			case 15: // unknown topic is an actual misroute, not decoder rejection
				b.topic = "state.invalid-kind"
				reject(b, coreclient.ErrTopicReset)
			case 16: // source fault advances cursor, not the retained projection
				b.state = stateFenceDecode(t, coreprotocol.WorkspaceState{Fault: &coreprotocol.Fault{Code: "fuzz", Message: "source unavailable"}})
				commit(i, b, value, version)
			case 17: // fault plus value must fail atomically
				b.state.Fault = &coreprotocol.Fault{Code: "fuzz", Message: "must reject mixed state"}
				b.state = stateFenceDecode(t, b.state)
				reject(b, coreclient.ErrTopicReset)
			}
		}
	})
}
