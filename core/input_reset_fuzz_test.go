package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"crude/collaboration"
	"crude/coreprotocol"
	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	"crude/types"
	"crude/workspace"
)

const inputResetFuzzActions = 8

// All connections are fixture-owned. Only the input target starts inference,
// against its held loopback SSE response; settings never submits a model turn.
func inputResetConfig(url string) (types.AppSettings, *session.Config) {
	off := false
	app := types.AppSettings{
		LocalHistory: types.LocalHistorySettings{Enabled: "off"},
		Accounts: map[string]provider.Account{"fixture": {
			ID: "fixture", ProviderID: "openai", APIKey: "fixture-only",
			Connection: provider.Template{Name: "Fixture", ProviderURL: url, APIStandard: "openai", AuthType: provider.AuthProtocol},
		}},
		Agents: types.AgentsSettings{ActiveProfile: "default", Categories: map[string]types.CategorySettings{
			"default": {AccountID: "fixture", Model: "parent-0", ContextRetentionTokens: coreprotocol.Ptr(1000)},
		}},
	}
	agents := session.DefaultAgentsConfig()
	profile := agents.Profiles["default"]
	profile.MultiAgent = &off
	profile.Categories[session.CategoryDefault] = session.CategoryConfig{AccountID: "fixture", Model: "parent-0"}
	agents.Profiles["default"] = profile
	return app, &session.Config{Agents: agents, Accounts: app.Accounts}
}

// The SDK deliberately supplies its negotiated Owner rather than accepting a
// forged request.Owner. Exercise that negative at the public Connection.Call
// ingress, not by pretending CallRequest sends an arbitrary owner on the wire.
func inputResetWrongOwner(t *testing.T, r *Runtime, w *resourceWire, ws, id, method string, input any) {
	t.Helper()
	r.mu.Lock()
	c := r.clients[w.owner.ClientID]
	r.mu.Unlock()
	owner := w.owner
	owner.Epoch++
	out := c.Call(t.Context(), coreprotocol.Request{ID: id, Owner: owner, WorkspaceID: ws, Method: method, Payload: mustTestPayload(input)})
	if out.Error == nil || out.Error.Code != "invalid_owner" {
		t.Fatalf("replacement attachment owner was accepted: %+v", out.Error)
	}
}

func FuzzCoreInputAdmissionReceipts(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7})
	f.Add([]byte{7, 0, 7, 1, 2, 0, 6, 1})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, actions []byte) {
		started, release := make(chan struct{}), make(chan struct{})
		var startedOnce, releaseOnce sync.Once
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"held turn\"}}]}\n\n")
			w.(http.Flusher).Flush()
			startedOnce.Do(func() { close(started) })
			select {
			case <-request.Context().Done():
			case <-release:
			}
		}))
		t.Cleanup(server.Close)
		var store *collaboration.Store
		r, root := featureRuntime(t, func(ws *workspace.Workspace) {
			app, cfg := inputResetConfig(server.URL)
			ws.SetAppDefaults(app)
			ws.SetBackendConfig(cfg, t.TempDir())
			store = ws.Collaboration
		})
		// Cleanup ordering: wires close, SSE releases, Runtime.Close joins all
		// owned backend work, and only then does the HTTP fixture close.
		t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
		peers := [2]*resourceWire{newResourceWire(t, r, "input-author"), newResourceWire(t, r, "input-peer")}
		var attached coreprotocol.AttachResult
		peers[0].call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached)
		peers[1].call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, nil)
		ws := attached.WorkspaceID
		var created workspace.NewSessionResult
		peers[0].call(t, ws, coreprotocol.NewSession, coreprotocol.InvocationSessionRequest{}, &created)
		first := coreprotocol.InputRequest{AgentID: created.OrchID, Text: "Hold this first input", DraftRevision: 1}
		var admitted coreprotocol.InputResult
		firstResponse := featureCall(t, peers[0], ws, "first", coreprotocol.SubmitInput, first, &admitted)
		if admitted.Status != workspace.InputAdmissionAccepted || admitted.Error != "" || admitted.Receipt.SessionID != created.SessionID || admitted.Receipt.AgentID != created.OrchID || admitted.Receipt.InputID != "client-input-author-first" || admitted.Receipt.EventID != "" || admitted.Receipt.RunID != "" || admitted.Receipt.DraftRevision != 1 || admitted.Receipt.OwnerEpoch == 0 || admitted.Receipt.TurnGeneration == 0 || admitted.Receipt.ContextRevision == 0 || admitted.Receipt.AcceptedAt.IsZero() {
			t.Fatalf("fresh input lost exact admission: %+v", admitted)
		}
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("first input never reached loopback provider")
		}
		participant, err := store.Agent(t.Context(), created.OrchID)
		if err != nil || participant.CurrentRunID == "" || participant.RuntimeEpoch != admitted.Receipt.OwnerEpoch {
			t.Fatalf("accepted input did not own a live run: %+v, %v", participant, err)
		}
		type acceptedNote struct {
			peer     int
			id       string
			input    coreprotocol.InputRequest
			response coreprotocol.Response
		}
		last := acceptedNote{peer: 0, id: "first", input: first, response: firstResponse}
		// These maps are intent oracles, not derived by replaying receipts.
		wantText := map[string]int{first.Text: 1}
		wantEvents := make(map[string]string)
		check := func() {
			t.Helper()
			page, err := store.Page(t.Context(), collaboration.PageQuery{RecipientID: created.OrchID, Kind: collaboration.EventHumanSteering, Limit: 20})
			if err != nil || len(page.Events) != len(wantEvents) {
				t.Fatalf("steering event count = %d, want %d, err=%v", len(page.Events), len(wantEvents), err)
			}
			seen := make(map[string]bool)
			for _, event := range page.Events {
				text, ok := wantEvents[event.ID]
				if !ok || seen[event.ID] || event.Body != text || event.RunID != participant.CurrentRunID || event.Origin != collaboration.OriginHuman {
					t.Fatalf("duplicate or wrong-run steering: %+v", event)
				}
				seen[event.ID] = true
			}
			var changes coreprotocol.AgentChangesResult
			peers[0].call(t, ws, coreprotocol.AgentChanges, coreprotocol.AgentChangesRequest{}, &changes)
			gotText := make(map[string]int)
			for _, delta := range changes.Agents {
				if delta.State.ID == created.OrchID {
					for _, message := range delta.Conversation.Upserts {
						if message.Role == workspace.MessageRoleUser {
							gotText[message.Content]++
						}
					}
				}
			}
			if !reflect.DeepEqual(gotText, wantText) {
				t.Fatalf("canonical human inputs = %v, want %v", gotText, wantText)
			}
		}
		check()
		for i, action := range actions[:min(len(actions), inputResetFuzzActions)] {
			id := fmt.Sprintf("input-%d", i)
			note := coreprotocol.InputRequest{AgentID: created.OrchID, Text: fmt.Sprintf("correction-%d-%d", i, action), DraftRevision: uint64(i + 2)}
			switch action % 8 {
			case 0, 7: // Shared-workspace peers may legitimately steer the same run.
				peer := int(action % 8 / 7)
				var result coreprotocol.InputResult
				out := featureCall(t, peers[peer], ws, id, coreprotocol.SubmitInput, note, &result)
				receipt := result.Receipt
				if result.Status != workspace.InputAdmissionSteered || result.Error != "" || receipt.SessionID != created.SessionID || receipt.AgentID != created.OrchID || receipt.InputID != "client-"+peers[peer].owner.ClientID+"-"+id || receipt.EventID == "" || receipt.RunID != participant.CurrentRunID || receipt.OwnerEpoch != participant.RuntimeEpoch || receipt.DraftRevision != note.DraftRevision || receipt.ContextRevision != 0 || receipt.TurnGeneration != 0 || receipt.AcceptedAt.IsZero() {
					t.Fatalf("steering lost exact run/draft receipt: %+v", result)
				}
				if _, duplicate := wantEvents[receipt.EventID]; duplicate {
					t.Fatal("fresh identity reused another input's event")
				}
				wantEvents[receipt.EventID], wantText[note.Text] = note.Text, 1
				last = acceptedNote{peer: peer, id: id, input: note, response: out}
			case 1:
				out := featureCall(t, peers[last.peer], ws, last.id, coreprotocol.SubmitInput, last.input, nil)
				if out.Error != nil || !bytes.Equal(out.Payload, last.response.Payload) {
					t.Fatal("same identity changed its immutable receipt")
				}
			case 2:
				changed := last.input
				changed.Text += " changed"
				out := featureCall(t, peers[last.peer], ws, last.id, coreprotocol.SubmitInput, changed, nil)
				if out.Error == nil {
					t.Fatal("changed receipt preimage was accepted")
				}
			case 3:
				note.AgentID = "not-an-agent"
				if out := featureCall(t, peers[0], ws, id, coreprotocol.SubmitInput, note, nil); out.Error == nil {
					t.Fatal("unknown agent admitted input")
				}
			case 4:
				if out := featureCall(t, peers[0], ws, id, coreprotocol.SubmitInput, coreprotocol.Empty{}, nil); out.Error == nil {
					t.Fatal("invalid typed input payload admitted")
				}
			case 5:
				note.Text = " \t\n"
				if out := featureCall(t, peers[0], ws, id, coreprotocol.SubmitInput, note, nil); out.Error == nil || out.Error.Code != "invalid_input" {
					t.Fatal("empty human input admitted")
				}
			case 6:
				inputResetWrongOwner(t, r, peers[0], ws, id, coreprotocol.SubmitInput, note)
			}
			check()
		}
		peers[0].call(t, ws, coreprotocol.CancelAgent, coreprotocol.AgentRequest{AgentID: created.OrchID, Generation: admitted.Receipt.TurnGeneration}, nil)
	})
}

func inputResetEdit(path []string, value any) types.SettingEdit {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err) // All callers supply fixed scalar types.
	}
	return types.SettingEdit{Path: path, After: raw}
}

func FuzzCoreSessionSettingsReset(f *testing.F) {
	f.Add([]byte{0, 2, 1, 2, 0, 5, 6, 7})
	f.Add([]byte{3, 4, 0, 1, 5, 2, 1, 7})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, actions []byte) {
		app, cfg := inputResetConfig("http://127.0.0.1:1")
		r, root := featureRuntime(t, func(ws *workspace.Workspace) {
			ws.SetAppDefaults(app)
			ws.SetBackendConfig(cfg, t.TempDir())
			ws.OnAppPreferencesCommit = func(edits []types.SettingEdit) (types.AppSettings, error) {
				next := types.CloneAppSettings(app)
				if err := types.ApplySettingsEdits(&next, edits); err != nil {
					return next, err
				}
				app = next
				return types.CloneAppSettings(next), nil
			}
		})
		w := newResourceWire(t, r, "reset-owner")
		var attached coreprotocol.AttachResult
		w.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached)
		ws := attached.WorkspaceID
		var created workspace.NewSessionResult
		w.call(t, ws, coreprotocol.NewSession, coreprotocol.InvocationSessionRequest{}, &created)
		var backend *session.Session
		if err := r.Each(t.Context(), func(ws *workspace.Workspace) { backend = ws.BackendSession(created.SessionID) }); err != nil || backend == nil {
			t.Fatalf("missing real session: %v", err)
		}
		w.call(t, ws, coreprotocol.SessionCapabilityToggle, coreprotocol.SessionSettingsMutationRequest{SessionID: created.SessionID, Capability: &coreprotocol.SessionCapabilityChange{ID: "fuzz-separate-optout", Enabled: false}}, nil)
		parentModel, parentTokens := "parent-0", 1000
		overrideModel, overrideTokens, maxChildren := "", 0, 0
		var pinned bool
		check := func() {
			t.Helper()
			var got coreprotocol.SessionSettingsResult
			w.call(t, ws, coreprotocol.SessionSettingsRead, coreprotocol.SessionSettingsRequest{SessionID: created.SessionID}, &got)
			wantModel, wantTokens := parentModel, parentTokens
			if pinned {
				wantModel, wantTokens = overrideModel, overrideTokens
			}
			category := got.Effective.Value.Agents.Categories["default"]
			if got.Effective.Value.Model != wantModel || category.ContextRetentionTokens == nil || *category.ContextRetentionTokens != wantTokens || got.Base.Value.Model != parentModel {
				t.Fatalf("resolved settings do not follow explicit layer: model=%q category=%+v base=%q want=%q/%d parent=%q", got.Effective.Value.Model, category, got.Base.Value.Model, wantModel, wantTokens, parentModel)
			}
			flags, err := sessiondb.LoadSessionFlags(backend.DB(), created.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			stored, err := sessiondb.LoadAgentSettingOverrides(backend.DB(), created.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if pinned {
				local := stored["default"]
				if got.Overrides["default"].Model != overrideModel || local.Model != overrideModel || local.ContextRetentionTokens == nil || *local.ContextRetentionTokens != overrideTokens || got.Effective.MaxSubAgents == nil || *got.Effective.MaxSubAgents != maxChildren || flags.MaxSubAgents == nil || *flags.MaxSubAgents != maxChildren || flags.MultiAgent == nil || *flags.MultiAgent || flags.CodeIntelligence == nil || *flags.CodeIntelligence || flags.TurnLogs == nil || *flags.TurnLogs {
					t.Fatalf("session save lost persisted scalar choices: %+v %+v", stored, flags)
				}
			} else if len(got.Overrides) != 0 || len(stored) != 0 || got.Effective.MainAgent != "" || got.Effective.MultiAgent != nil || got.Effective.MaxSubAgents != nil || got.Effective.CodeIntelligence != nil || got.Effective.TurnLogs != nil || !reflect.DeepEqual(flags, sessiondb.SessionFlags{}) {
				t.Fatalf("Reset did not clear preference rows/flags: %+v %+v", got, flags)
			}
			if !reflect.DeepEqual(got.Effective.DisabledCapabilities, []string{"fuzz-separate-optout"}) {
				t.Fatal("settings Reset consumed separate capability opt-out")
			}
		}
		save := func(id string, n int) {
			t.Helper()
			overrideModel, overrideTokens, maxChildren = fmt.Sprintf("session-%d", n), 2000+n*100, n%5
			edits := []types.SettingEdit{
				inputResetEdit([]string{"overrides", "default", "Model"}, overrideModel),
				inputResetEdit([]string{"overrides", "default", "context_retention_tokens"}, overrideTokens),
				inputResetEdit([]string{"multi_agent"}, false),
				inputResetEdit([]string{"code_intelligence"}, false),
				inputResetEdit([]string{"turn_logs"}, false),
				inputResetEdit([]string{"max_sub_agents"}, maxChildren),
			}
			featureCall(t, w, ws, id, coreprotocol.SessionSettingsSave, coreprotocol.SessionSettingsMutationRequest{SessionID: created.SessionID, Edits: edits}, &coreprotocol.SessionSettingsResult{})
			pinned = true
			check()
		}
		resetRequest := coreprotocol.SessionSettingsMutationRequest{SessionID: created.SessionID}
		lastResetID := "initial-reset"
		save("initial-save", 0)
		var resetResult coreprotocol.SessionSettingsResult
		lastReset := featureCall(t, w, ws, lastResetID, coreprotocol.SessionSettingsReset, resetRequest, &resetResult)
		pinned = false
		check()
		for i, action := range actions[:min(len(actions), inputResetFuzzActions)] {
			id := fmt.Sprintf("settings-%d", i)
			switch action % 8 {
			case 0:
				save(id, i+1)
			case 1:
				lastResetID = id
				lastReset = featureCall(t, w, ws, id, coreprotocol.SessionSettingsReset, resetRequest, &resetResult)
				pinned = false
			case 2:
				parentModel, parentTokens = fmt.Sprintf("parent-%d", i+1), 3000+i*100
				featureCall(t, w, ws, id, coreprotocol.ApplicationPreferencesSave, coreprotocol.ApplicationPreferencesRequest{ProxyURL: coreprotocol.SecretString{Keep: true}, Edits: []types.SettingEdit{
					inputResetEdit([]string{"agents", "Categories", "default", "Model"}, parentModel),
					inputResetEdit([]string{"agents", "Categories", "default", "context_retention_tokens"}, parentTokens),
				}}, &coreprotocol.ApplicationPreferencesResult{})
			case 3:
				out := featureCall(t, w, ws, id, coreprotocol.SessionSettingsReset, coreprotocol.SessionSettingsMutationRequest{SessionID: "wrong-session"}, nil)
				if out.Error == nil || out.Error.Code != "session_missing" {
					t.Fatal("Reset acted on a different session owner")
				}
			case 4:
				out := featureCall(t, w, ws, id, coreprotocol.SessionSettingsReset, coreprotocol.SessionSettingsMutationRequest{SessionID: created.SessionID, Edits: []types.SettingEdit{inputResetEdit([]string{"max_sub_agents"}, "wrong-type")}}, nil)
				if out.Error == nil || out.Error.Code != "invalid_request" {
					t.Fatal("Reset ignored invalid mutation payload")
				}
			case 5:
				out := featureCall(t, w, ws, lastResetID, coreprotocol.SessionSettingsReset, resetRequest, nil)
				if out.Error != nil || !bytes.Equal(out.Payload, lastReset.Payload) {
					t.Fatal("Reset replay changed its original result")
				}
			case 6:
				inputResetWrongOwner(t, r, w, ws, id, coreprotocol.SessionSettingsReset, resetRequest)
			case 7:
				// A changed body under a previous Reset identity is not a fresh reset.
				out := featureCall(t, w, ws, lastResetID, coreprotocol.SessionSettingsReset, coreprotocol.SessionSettingsMutationRequest{SessionID: "different-owner"}, nil)
				if out.Error == nil {
					t.Fatal("Reset identity accepted a changed preimage")
				}
			}
			check()
		}
	})
}
