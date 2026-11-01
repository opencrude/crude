package viewmodel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/coreprotocol"
	"crude/session"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
)

// The actual session deck, connected core and provider request must agree:
// a named profile can be edited without modifying the built-in role.
func TestConnectedNamedAgentSpeedJourney(t *testing.T) {
	type request struct {
		Model string `json:"model"`
		Tier  string `json:"service_tier"`
	}
	requests := make(chan request, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body request
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	runtime := remoteJourneyRuntimeConfig(t, server.URL, func(cfg *session.Config) {
		profile := cfg.Agents.Profiles["default"]
		profile.Categories[session.CategoryResearcher] = session.CategoryConfig{AccountID: "test", Model: "role-model", ServiceTier: "standard"}
		cfg.Agents.Profiles["default"] = profile
		cfg.Agents.AgentProfiles["writer"] = types.AgentProfileSettings{Category: "crafter", Model: "writer-model"}
	})
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool { return two.app.ws.AgentMgr().Get(id) != nil })
	// Create the named agent after the session is already running. The settings
	// owner must publish the catalog to both shells and the existing session.
	profile, err := json.Marshal(types.AgentProfileSettings{Category: "researcher", Model: "review-model"})
	if err != nil {
		t.Fatal(err)
	}
	saveProfile := one.app.ws.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{{
		Path: []string{"agents", "agent_profiles", "reviewer"}, After: profile,
	}})
	var saveErr error
	runAppCommand(t, one.app, func() tea.Msg {
		saveErr = saveProfile().Err
		return nil
	})
	if saveErr != nil {
		t.Fatal(saveErr)
	}
	remoteJourneyUntil(t, shells, func() bool {
		for _, shell := range shells {
			owner := shell.app.ws.SessionForAgent(id)
			if owner == nil || owner.Settings.Effective.Agents.AgentProfiles["reviewer"].Model != "review-model" {
				return false
			}
		}
		return true
	})
	one.app.Input.SetInput("/settings")
	one.app.setFocusInput()
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cmd)
	if one.app.sessionSettings() == nil {
		t.Fatal("/settings did not open the session deck")
	}

	invoke := func(suffix string, op ui.ControlOperation) {
		t.Helper()
		in, d, node := invokeDeckControl(t, one, suffix, op)
		result := requestShellForTest(t, runtime, shells, one, "shell.control", in, func(e *coreprotocol.ControlEnvelope) {
			e.Descriptor = d
			e.Descriptor.ID = "shell.control"
			e.Request.Target.ResourceID = in.ComponentID
			e.Request.Target.SessionID = node.Target.SessionID
			e.Request.Preconditions = control.Preconditions{Revision: &node.Revision, OpenEpoch: &node.OpenEpoch}
		})
		if result.State != control.Applied {
			t.Fatalf("%s: %+v", suffix, result)
		}
	}
	invoke("session-settings/form/profile/reviewer", ui.ControlOperation{Verb: "activate"})
	invoke("session-settings/form/service-tier", ui.ControlOperation{Verb: "select", ItemID: "fast"})
	invoke("session-settings/actions/apply", ui.ControlOperation{Verb: "activate"})
	invoke("session-settings/form/main-agent", ui.ControlOperation{Verb: "activate"})
	invoke("session-settings/model-list", ui.ControlOperation{Verb: "activate", ItemID: "profile/reviewer"})
	// Selecting a root profile changes authority; Save is a human action.
	save := func() {
		t.Helper()
		result := one.app.sessionSettings().HandleAction(ui.EventContext{}, ui.Action{ID: "session-settings.save"})
		_, saveCmd := one.app.Update(result.Cmd())
		runAppCommand(t, one.app, saveCmd)
	}
	save()
	remoteJourneyUntil(t, shells, func() bool {
		for _, shell := range shells {
			owner := shell.app.ws.SessionForAgent(id)
			if owner == nil || owner.Settings.Effective.SessionMainAgent != "profile/reviewer" ||
				owner.Settings.Effective.FastMode != types.FastModeOn {
				return false
			}
		}
		return true
	})
	owner := one.app.ws.SessionForAgent(id)
	if owner.Settings.Overrides["profile/reviewer"].ServiceTier != "fast" ||
		owner.Settings.Overrides["researcher"].ServiceTier != "" ||
		owner.Settings.Overrides["profile/writer"].ServiceTier != "" {
		t.Fatalf("wrong session override: %+v", owner.Settings.Overrides)
	}

	send := func(text, wantTier string) {
		t.Helper()
		one.app.Input.SetInput(text)
		one.app.setFocusInput()
		_, command := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		runAppCommand(t, one.app, command)
		select {
		case got := <-requests:
			if got.Model != "review-model" || got.Tier != wantTier {
				t.Fatalf("provider request = %+v, want review-model / %s", got, wantTier)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("provider request was not sent")
		}
		remoteJourneyUntil(t, shells, func() bool { return one.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted })
	}
	send("first request", "fast")

	one.app.openSessionSettings()
	invoke("session-settings/form/profile/reviewer", ui.ControlOperation{Verb: "activate"})
	invoke("session-settings/form/service-tier", ui.ControlOperation{Verb: "select", ItemID: "standard"})
	invoke("session-settings/actions/apply", ui.ControlOperation{Verb: "activate"})
	save()
	remoteJourneyUntil(t, shells, func() bool {
		for _, shell := range shells {
			if shell.app.ws.SessionForAgent(id).Settings.Effective.FastMode != types.FastModeOff {
				return false
			}
		}
		return true
	})
	send("second request", "default")

	removeProfile := one.app.ws.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{{
		Path: []string{"agents", "agent_profiles", "reviewer"},
	}})
	runAppCommand(t, one.app, func() tea.Msg {
		saveErr = removeProfile().Err
		return nil
	})
	if saveErr != nil {
		t.Fatal(saveErr)
	}
	remoteJourneyUntil(t, shells, func() bool {
		for _, shell := range shells {
			owner := shell.app.ws.SessionForAgent(id)
			if owner == nil {
				return false
			}
			if _, exists := owner.Settings.Effective.Agents.AgentProfiles["reviewer"]; exists {
				return false
			}
		}
		return true
	})
	one.app.openSessionSettings()
	if _, exists := one.app.sessionSettings().ToSettings().Agents.AgentProfiles["reviewer"]; exists {
		t.Fatal("reopened settings retained the removed named agent")
	}
}
