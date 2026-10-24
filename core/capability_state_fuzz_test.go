package core

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"crude/coreprotocol"
	"crude/session"
	"crude/sessiondb"
	"crude/types"
	captypes "crude/types/capability"
	"crude/workspace"
)

// FuzzCoreCapabilities uses only reviewed fixture-owned local bytes. All commands
// cross the framed SDK ingress except the forged-owner negative (the SDK replaces
// supplied owners). No activation submits a model turn or starts an MCP transport.
func FuzzCoreCapabilities(f *testing.F) {
	for _, seed := range [][]byte{{}, {0, 8, 1, 9, 2, 10, 3, 7}, {4, 12, 5, 13, 6, 14, 2, 3}, {20, 28, 2, 10, 3, 2, 3, 7}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, actions []byte) {
		source := t.TempDir()
		names := [2]string{"guide-a", "guide-b"}
		ids := [2]string{"local:guide-a", "local:guide-b"}
		scopes := [2]captypes.Scope{captypes.ScopeApplication, captypes.ScopeWorkspace}
		var authored [2][]byte
		for n, name := range names {
			dir := filepath.Join(source, name)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			authored[n] = fmt.Appendf(nil, "---\nname: %s\ndescription: Local semantic fixture.\n---\n\nOnly fixture instructions %d.\n", name, n)
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), authored[n], 0600); err != nil {
				t.Fatal(err)
			}
		}
		commits := 0
		r, root := featureRuntime(t, func(ws *workspace.Workspace) {
			app, cfg := inputResetConfig("http://127.0.0.1:1") // inert synthetic account; no inference
			app.Capabilities.Sources = map[string]captypes.SourceConfig{"local": {ID: "local", Name: "Fixture", Kind: "local", Location: source, Trusted: true}}
			ws.SetAppDefaults(app)
			ws.SetBackendConfig(cfg, t.TempDir())
			if _, err := ws.CapabilitySnapshot(t.Context()); err != nil {
				t.Fatal(err)
			}
			ws.OnAppPreferencesCommit = func(edits []types.SettingEdit) (types.AppSettings, error) {
				next := types.CloneAppSettings(app)
				if err := types.ApplySettingsEdits(&next, edits); err != nil {
					return next, err
				}
				app = next
				commits++
				return types.CloneAppSettings(app), nil
			}
		})
		w := newResourceWire(t, r, "capability-owner")
		var attached coreprotocol.AttachResult
		w.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached)
		ws := attached.WorkspaceID
		var created workspace.NewSessionResult
		w.call(t, ws, coreprotocol.NewSession, coreprotocol.InvocationSessionRequest{}, &created)
		var backend *session.Session
		if err := r.Each(t.Context(), func(ws *workspace.Workspace) { backend = ws.BackendSession(created.SessionID) }); err != nil || backend == nil {
			t.Fatalf("missing session: %v", err)
		}
		var discovered coreprotocol.CapabilityResult
		w.call(t, ws, coreprotocol.CapabilityAction, coreprotocol.CapabilityActionRequest{Action: captypes.Action{Operation: "discover", Kind: captypes.KindSkill, Query: "guide", Arguments: map[string]string{"source_id": "local"}}}, &discovered)
		if len(discovered.Entries) != 2 {
			t.Fatalf("local discovery = %+v", discovered.Entries)
		}
		for _, id := range ids {
			if !slices.ContainsFunc(discovered.Entries, func(e captypes.Entry) bool { return e.ID == id && e.Kind == captypes.KindSkill }) {
				t.Fatal("discovery lost authored identity")
			}
		}
		var installed, disabled [2]bool
		var stale [2]captypes.Action
		var paths [2]string
		var lastInput any
		var lastID, lastMethod string
		var lastResponse coreprotocol.Response
		wantCommits := 0
		check := func() {
			t.Helper()
			var got coreprotocol.SettingsFetchResult
			w.call(t, ws, coreprotocol.SettingsFetch, coreprotocol.Empty{}, &got)
			appCount, effectiveCount := 0, 0
			for n, exists := range installed {
				if exists {
					effectiveCount++
					if n == 0 {
						appCount++
					}
				}
			}
			if commits != wantCommits || len(got.Application.Capabilities.Skills) != appCount || len(got.Effective.Capabilities.Skills) != effectiveCount {
				t.Fatal("registration count or application commit count diverged")
			}
			// Public source locations are URL-redacted (including '#' in test
			// directory names); verify the actual retained preimage at its owner.
			var retainedSource captypes.SourceConfig
			if err := r.Each(t.Context(), func(ws *workspace.Workspace) { retainedSource = ws.AppDefaults().Capabilities.Sources["local"] }); err != nil {
				t.Fatal(err)
			}
			if retainedSource.Location != source || !retainedSource.Trusted || !got.Application.Capabilities.Sources["local"].Trusted {
				t.Fatal("passive operation consumed source settings")
			}
			var wantDisabled []string
			for n, id := range ids {
				_, appBound := got.Application.Capabilities.Skills[id]
				if appBound != (n == 0 && installed[n]) {
					t.Fatal("workspace binding leaked into application")
				}
				if value, ok := got.Effective.Capabilities.Skills[id]; ok != installed[n] {
					t.Fatal("missing or invented registration")
				} else if ok {
					if value.ID != id || value.Scope != scopes[n] || value.Name != names[n] || value.Source != "local" || !value.Managed || value.Disabled || value.Path == filepath.Join(source, names[n]) {
						t.Fatalf("wrong managed binding: %+v", value)
					}
					if paths[n] != "" && value.Path != paths[n] {
						t.Fatal("identical reviewed bytes changed artifact identity")
					}
					paths[n] = value.Path
					if data, err := os.ReadFile(filepath.Join(value.Path, "SKILL.md")); err != nil || !bytes.Equal(data, authored[n]) {
						t.Fatal("installed bytes differ from authored source")
					}
				}
				if disabled[n] {
					wantDisabled = append(wantDisabled, id)
				}
			}
			var gate coreprotocol.SessionSettingsResult
			w.call(t, ws, coreprotocol.SessionSettingsRead, coreprotocol.SessionSettingsRequest{SessionID: created.SessionID}, &gate)
			stored, err := sessiondb.LoadDisabledCapabilities(backend.DB(), created.SessionID)
			if err != nil || !slices.Equal(gate.Effective.DisabledCapabilities, wantDisabled) || !slices.Equal(stored, wantDisabled) || !slices.Equal(backend.DisabledCapabilities(), wantDisabled) {
				t.Fatal("session gate projection, live owner or durable rows diverged")
			}
		}
		preview := func(id string, n int) captypes.Action {
			t.Helper()
			a := captypes.Action{Operation: "install", Kind: captypes.KindSkill, ID: ids[n], Scope: scopes[n]}
			if installed[n] {
				a.Operation = "update"
			}
			var p coreprotocol.CapabilityPreviewResult
			featureCall(t, w, ws, id, coreprotocol.CapabilityAcquisitionPreview, coreprotocol.CapabilityActionRequest{Action: a}, &p)
			if p.Preview.Token == "" || p.Preview.ID != ids[n] || p.Preview.Scope != scopes[n] || p.Preview.Candidate.Name != names[n] {
				t.Fatal("preview lost exact source/binding identity")
			}
			a.Preview = &p.Preview
			return a
		}
		// A valid reviewed prefix guarantees both real binding owners are exercised,
		// even for empty inputs; preview must remain entirely passive.
		for n := range ids {
			stale[n] = preview(fmt.Sprintf("preview-%d", n), n)
			check()
			lastID, lastMethod = fmt.Sprintf("install-%d", n), coreprotocol.CapabilityAction
			lastInput = coreprotocol.CapabilityActionRequest{Action: stale[n]}
			lastResponse = featureCall(t, w, ws, lastID, lastMethod, lastInput, &coreprotocol.CapabilityResult{})
			installed[n] = true
			if n == 0 {
				wantCommits++
			}
			check()
		}
		for i, action := range actions[:min(len(actions), 8)] {
			id, n := fmt.Sprintf("action-%d", i), int(action/8)%2
			switch action % 8 {
			case 0:
				a := preview(id+"-preview", n)
				check()
				lastID, lastMethod, lastInput = id, coreprotocol.CapabilityAction, coreprotocol.CapabilityActionRequest{Action: a}
				lastResponse = featureCall(t, w, ws, lastID, lastMethod, lastInput, &coreprotocol.CapabilityResult{})
				if n == 0 {
					wantCommits++
				}
			case 1:
				if out := featureCall(t, w, ws, id, coreprotocol.CapabilityAction, coreprotocol.CapabilityActionRequest{Action: stale[n]}, nil); out.Error == nil {
					t.Fatal("installed binding accepted its pre-install preview")
				}
			case 2:
				disabled[n] = !disabled[n]
				lastID, lastMethod = id, coreprotocol.SessionCapabilityToggle
				lastInput = coreprotocol.SessionSettingsMutationRequest{SessionID: created.SessionID, Capability: &coreprotocol.SessionCapabilityChange{ID: ids[n], Enabled: !disabled[n]}}
				lastResponse = featureCall(t, w, ws, lastID, lastMethod, lastInput, &coreprotocol.SessionSettingsResult{})
				out := featureCall(t, w, ws, id+"-load", coreprotocol.SkillInput, coreprotocol.SkillInputRequest{AgentID: created.OrchID, ID: ids[n]}, nil)
				if (out.Error != nil) != disabled[n] {
					t.Fatalf("skill loading ignored session gate: %+v", out.Error)
				}
			case 3:
				var before, after coreprotocol.SessionSettingsResult
				w.call(t, ws, coreprotocol.SessionSettingsRead, coreprotocol.SessionSettingsRequest{SessionID: created.SessionID}, &before)
				out := featureCall(t, w, ws, lastID, lastMethod, lastInput, nil)
				w.call(t, ws, coreprotocol.SessionSettingsRead, coreprotocol.SessionSettingsRequest{SessionID: created.SessionID}, &after)
				if out.Error != nil || !bytes.Equal(out.Payload, lastResponse.Payload) || !reflect.DeepEqual(before, after) {
					t.Fatal("exact receipt replay changed result or repeated session mutation")
				}
			case 4:
				a := preview(id+"-preview", n)
				if action&16 == 0 {
					a.Preview.Token = "invalid-token"
				} else {
					a.Scope = scopes[1-n] // exact scope preimage, not display metadata
				}
				if out := featureCall(t, w, ws, id, coreprotocol.CapabilityAction, coreprotocol.CapabilityActionRequest{Action: a}, nil); out.Error == nil {
					t.Fatal("changed preview preimage accepted")
				}
			case 5:
				a := captypes.Action{Operation: "install", ID: "local:missing", Kind: captypes.KindSkill, Scope: scopes[n]}
				if out := featureCall(t, w, ws, id, coreprotocol.CapabilityAcquisitionPreview, coreprotocol.CapabilityActionRequest{Action: a}, nil); out.Error == nil {
					t.Fatal("unknown catalog identity previewed")
				}
			case 6:
				inputResetWrongOwner(t, r, w, ws, id, coreprotocol.CapabilityAction, coreprotocol.CapabilityActionRequest{Action: stale[n]})
			case 7:
				if out := featureCall(t, w, ws, lastID, lastMethod, coreprotocol.Empty{}, nil); out.Error == nil {
					t.Fatal("receipt identity accepted a changed request body")
				}
			}
			check()
		}
	})
}
