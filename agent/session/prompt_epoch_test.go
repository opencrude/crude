package session

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/agent/hostshell"
	"crude/agent/provider"
	"crude/internal/providersim"
	captypes "crude/types/capability"
	"crude/types/setting"
)

type epochCall struct {
	provider.ChatRequest
	reply chan providersim.Turn
}
type epochJourney struct {
	*Session
	owner  *Orchestrator
	sender *mockEventSender
	calls  chan epochCall
}

func newEpochJourney(t *testing.T) *epochJourney {
	t.Helper()
	j := &epochJourney{calls: make(chan epochCall), sender: newMockEventSender()}
	stop := make(chan struct{})
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		if request.Model == "epoch-extractor" {
			return providersim.Text("<<<intent>>>\n- Retain request\n<<<work>>>\n- Reviewed earlier evidence\n<<<state>>>\n- Unchanged\n<<<continuation>>>\n- Continue current task")
		}
		call := epochCall{ChatRequest: simDecodeRequest[provider.ChatRequest](t, request), reply: make(chan providersim.Turn, 1)}
		select {
		case j.calls <- call:
		case <-stop:
			return providersim.HTTPError(providersim.InvalidRequest)
		}
		select {
		case reply := <-call.reply:
			return reply
		case <-stop:
			return providersim.HTTPError(providersim.InvalidRequest)
		}
	})
	cfg := modelAttributionConfig(server.BaseURL(providersim.Chat), "epoch-model")
	// Exhaust the transport budget so a gateway error exercises the run supervisor.
	account := cfg.Accounts["account"]
	account.Connection.Dialer = &provider.DialSettings{MaxAttempts: 1}
	cfg.Accounts["account"] = account
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	zero := 0
	role := profile.Categories[CategoryDefault]
	role.ContextCompactionPercent = &zero
	role.ModelContextWindow = 200000
	role.ModelMaxOutput = 2048
	profile.Categories[CategoryDefault] = role
	role.Model = "epoch-extractor"
	profile.Categories[CategoryErrand] = role
	cfg.Agents.AgentProfiles = map[string]setting.AgentProfile{"builder": {Category: CategoryCrafter, Description: "Build <components>"}}
	s, err := NewWithSessionDir("prompt-epoch", cfg, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	j.Session = s
	s.SetWorkDir(t.TempDir())
	attachDelegationTestCollaboration(t, s)
	t.Cleanup(func() { close(stop); s.Close() })
	j.owner, err = s.NewOrchestrator(j.sender)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func (j *epochJourney) next(t *testing.T) epochCall {
	t.Helper()
	timeout := time.After(12 * time.Second)
	var last any
	for {
		select {
		case call := <-j.calls:
			return call
		case event := <-j.sender.ch:
			if event.typ == "recovery" || event.typ == "error" {
				last = event.data
			}
			if event.typ == "done" || event.typ == "blocking" {
				t.Fatalf("unexpected %s before next request: %+v (last recovery: %+v)", event.typ, event.data, last)
			}
		case <-timeout:
			t.Fatalf("no provider request; last recovery: %+v", last)
		}
	}
}

func (j *epochJourney) start(t *testing.T, text string) epochCall {
	t.Helper()
	if !j.owner.SubmitInput(provider.PlainInput(text)) {
		t.Fatal("input not admitted")
	}
	return j.next(t)
}

func (c epochCall) answer(text string) {
	c.reply <- providersim.Text(text)
}

func (c epochCall) tool(name, args string) {
	c.reply <- simTool(name+"-call", name, args)
}

func (j *epochJourney) finish(t *testing.T, c epochCall, text string) {
	t.Helper()
	c.answer(text)
	waitTransitionTurn(t, j.sender, j.owner)
}

func setEpochDelegation(t *testing.T, s *Session, enabled bool) {
	t.Helper()
	cfg := s.SettingsSnapshot()
	cfg.SessionMultiAgent = &enabled
	cfg.Agents.Enabled = enabled
	if err := s.HandleSettingsSave(cfg); err != nil {
		t.Fatal(err)
	}
}

func assertEpochPrefix(t *testing.T, first, second provider.ChatRequest) {
	t.Helper()
	if !reflect.DeepEqual(first.Tools, second.Tools) {
		t.Errorf("declared tools changed (%d -> %d)", len(first.Tools), len(second.Tools))
	}
	if len(second.Messages) < len(first.Messages) {
		t.Fatal("request lost prior messages")
	}
	for i, m := range first.Messages {
		if !reflect.DeepEqual(m, second.Messages[i]) {
			t.Errorf("wire prefix changed at message %d (%s)", i, m.Role)
			break
		}
	}
}

func assertEpochUpdate(t *testing.T, messages []provider.ChatMessage, fragments ...string) {
	t.Helper()
	var updates []string
	for _, m := range messages {
		if m.Role == "user" && strings.HasPrefix(m.ContentString(), "<settings_update>") {
			updates = append(updates, m.ContentString())
		}
	}
	if len(fragments) == 0 && len(updates) == 0 {
		return
	}
	if len(updates) != 1 || len(fragments) == 0 {
		t.Fatalf("got %d settings updates, expected sections %v", len(updates), fragments)
	}
	for _, f := range fragments {
		if !strings.Contains(updates[0], f) {
			t.Errorf("missing %q in settings update", f)
		}
	}
}

func TestRootSettingChangesAppendContractsAcrossTurns(t *testing.T) {
	j := newEpochJourney(t)
	first := j.start(t, "Start alone.")
	j.finish(t, first, "Solo result.")
	setEpochDelegation(t, j.Session, true)
	second := j.start(t, "Work together.")
	assertEpochPrefix(t, first.ChatRequest, second.ChatRequest)
	assertEpochUpdate(t, second.Messages[len(first.Messages):], `<operating_mode type="orchestrator">`, `<profile name="builder"`, "Build &lt;components&gt;")
	j.finish(t, second, "Result with team.")
	setEpochDelegation(t, j.Session, false)
	third := j.start(t, "Continue alone.")
	assertEpochPrefix(t, second.ChatRequest, third.ChatRequest)
	assertEpochUpdate(t, third.Messages[len(second.Messages):], `<operating_mode type="solo">`, `name="available_agent_profiles" removed="true"`)
	j.finish(t, third, "Done.")
	for _, m := range j.activeContext {
		if strings.HasPrefix(m.ContentString(), "<settings_update>") && (m.EffectiveOrigin() != provider.OriginRuntimeContext || m.EffectiveVisibility() != provider.VisibilityContextOnly) {
			t.Fatal("settings impersonate user input")
		}
	}
}

// A model switch keeps history but starts a new system prompt. A prior update
// must not override the current settings carried by that prompt.
func TestRootChangedModelRetiresPreviousSettings(t *testing.T) {
	j := newEpochJourney(t)
	j.finish(t, j.start(t, "Start alone."), "Ready.")
	setEpochDelegation(t, j.Session, true)
	prior := j.start(t, "Delegate if needed.")
	assertEpochUpdate(t, prior.Messages, `type="orchestrator"`)
	j.finish(t, prior, "Ready.")
	settings := j.SettingsSnapshot()
	off := false
	settings.SessionMultiAgent = &off
	settings.Model = "epoch-second-model"
	settings.SessionOverrides = map[string]setting.Category{CategoryDefault: {Provider: "openrouter", Model: settings.Model, APIStandard: "openai"}}
	if err := j.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	next := j.start(t, "Continue on the new model.")
	if next.Model != settings.Model || !strings.Contains(next.Messages[0].ContentString(), `type="solo"`) {
		t.Fatal("new model did not start with current settings")
	}
	assertEpochUpdate(t, next.Messages[len(prior.Messages):], "instead of earlier settings updates")
	j.finish(t, next, "Finished.")
}

func TestRootShellChangeAppendsEnvironment(t *testing.T) {
	j := newEpochJourney(t)
	first := j.start(t, "Inspect environment.")
	j.finish(t, first, "Ready.")
	settings := ConfigToSettings(j.Config())
	shell := "not-installed-shell"
	for _, candidate := range hostshell.Detect() {
		if candidate.ID != j.agentShell().ID {
			shell = candidate.ID
			break
		}
	}
	settings.Agents.Shell = shell
	j.UpdateWorkspaceDefaults(setting.App{Accounts: j.Config().Accounts, Agents: settings.Agents})
	second := j.start(t, "Inspect again.")
	assertEpochPrefix(t, first.ChatRequest, second.ChatRequest)
	assertEpochUpdate(t, second.Messages[len(first.Messages):], `name="environment"`, `<shell>`)
	j.finish(t, second, "Done.")
}

func TestRootRetryKeepsPromptAndAnnouncesSettings(t *testing.T) {
	j := newEpochJourney(t)
	first := j.start(t, "Continue if the connection drops.")
	setEpochDelegation(t, j.Session, true)
	first.reply <- providersim.HTTPError(providersim.RequestTimeout).
		WithFaultMessage("stream error: stream disconnected before completion: stream closed before response.completed")
	next := j.next(t)
	assertEpochPrefix(t, first.ChatRequest, next.ChatRequest)
	assertEpochUpdate(t, next.Messages[len(first.Messages):], `type="orchestrator"`)
	j.finish(t, next, "Recovered.")
}

func TestRootCheckpointsFoldSettingsIntoNewSystem(t *testing.T) {
	for _, mode := range []string{"manual", "in-run"} {
		t.Run(mode, func(t *testing.T) {
			j := newEpochJourney(t)
			seed := j.start(t, "Inspect the evidence.")
			j.finish(t, seed, strings.Repeat("Historical evidence. ", 1800))
			call := j.start(t, "Finish the review.")
			setEpochDelegation(t, j.Session, true)
			if mode == "manual" {
				call.tool("get_plan", "{}")
				next := j.next(t)
				assertEpochUpdate(t, next.Messages[len(call.Messages):], `type="orchestrator"`)
				j.finish(t, next, "Ready.")
				if result := j.HandleCompact(); result.Error != "" || result.Cancelled || result.After == 0 {
					t.Fatalf("compact: %+v", result)
				}
				call = j.start(t, "Continue after compact.")
			} else {
				prepared, res := j.PrepareCompaction(j.owner.ID)
				if prepared.Run != nil || res.OperationID == "" {
					t.Fatalf("in-run compaction was not queued: %+v", res)
				}
				call.tool("get_plan", "{}")
				call = j.next(t)
			}
			if !strings.Contains(call.Messages[0].ContentString(), `<operating_mode type="orchestrator">`) {
				t.Fatal("checkpoint kept obsolete solo system prompt")
			}
			assertEpochUpdate(t, call.Messages)
			j.finish(t, call, "Finished.")
			later := j.start(t, "Another turn.")
			assertEpochPrefix(t, call.ChatRequest, later.ChatRequest)
			assertEpochUpdate(t, later.Messages)
			j.finish(t, later, "Done.")
		})
	}
}

type epochRuntime struct {
	sharingRuntime
	catalog chan bool
}

func (r epochRuntime) Prepare(_ context.Context, actor captypes.RuntimeActor) (captypes.RuntimeSnapshot, error) {
	r.catalog <- actor.CatalogOnly
	return captypes.RuntimeSnapshot{Tools: []captypes.Tool{{Name: "mcp__editor__change", Effect: captypes.EffectWrite, Scope: "workspace", Fingerprint: "fixture", InputSchema: json.RawMessage(`{"type":"object"}`)}}}, nil
}
