package settings

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"crude/agent/provider"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	captypes "crude/types/capability"
)

func capabilityFixture(t *testing.T, kind captypes.Kind) (*Model, *WorkspaceSettingsSurface, *ui.Host) {
	t.Helper()
	settings := captypes.Normalize(captypes.Settings{
		Skills:      map[string]captypes.SkillConfig{"vendor:guide": {ID: "vendor:guide", Name: "Guide", Path: "/managed/guide", Source: "vendor", Version: "revision-1", Digest: "digest-1", Mode: "automatic", Managed: true, Scope: captypes.ScopeApplication}},
		Connections: map[string]captypes.ConnectionConfig{"docs": {ID: "docs", Name: "Documentation", Transport: "http", URL: "https://example.test/mcp", Auth: "oauth", Scope: captypes.ScopeApplication, TimeoutSeconds: 30, Grants: map[string]captypes.ToolGrant{"existing": {Effect: captypes.EffectRead, Scope: "workspace", Fingerprint: "existing-schema"}}}},
		Sources:     map[string]captypes.SourceConfig{"vendor": {ID: "vendor", Name: "Vendor", Kind: "registry", Location: "https://example.test/registry", Scope: captypes.ScopeApplication, Trusted: true}},
	})
	model := New(theme.DefaultDark(), types.AppSettings{Capabilities: settings}, nil)
	model.SetCapabilitySnapshot(captypes.Snapshot{Generation: 1, Settings: settings,
		Entries:     []captypes.Entry{{ID: "vendor:guide", Kind: captypes.KindSkill, Name: "Guide", Description: "Verified workflow", Source: "vendor", Publisher: "Vendor", Version: "revision-1", Digest: "digest-1", Path: "/managed/guide", Installed: true, Managed: true, Origin: captypes.ScopeApplication, State: "available"}, {ID: "docs", Kind: captypes.KindMCP, Name: "Documentation", Origin: captypes.ScopeApplication, Installed: true, State: "connected"}},
		Connections: []captypes.ConnectionState{{ID: "docs", State: "connected", Tools: []captypes.Tool{{Name: "mcp__docs__lookup", OriginalName: "lookup", ConnectionID: "docs", Description: "Look up a record", InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`), Fingerprint: "lookup-schema", Reason: "Needs explicit effect and scope grant"}, {Name: "mcp__docs__write", OriginalName: "write", ConnectionID: "docs", Fingerprint: "write-schema", Effect: captypes.EffectWrite, Scope: "workspace", Selected: true}}, Resources: []captypes.Resource{{ConnectionID: "docs", URI: "docs://guide", Name: "Guide", MIMEType: "text/plain"}}, Prompts: []captypes.Prompt{{ConnectionID: "docs", Name: "explain", Description: "Explain a topic", Arguments: []captypes.PromptArgument{{Name: "topic", Required: true}}}}, Logs: []string{"Connected", "Authorization [redacted]"}}},
	})
	root := NewSurface(theme.DefaultDark(), model)
	index := catSkills
	if kind == captypes.KindMCP {
		index = catMCP
	}
	model.SelectCategory(index)
	root.capCatalog.switchKind(kind)
	host := ui.NewHost(theme.DefaultDark(), root)
	root.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
	host.Resize(130, 36)
	host.Layout()
	return model, root, host
}

func capabilityRequest(t *testing.T, result ui.EventResult) CapabilityRequestMsg {
	t.Helper()
	if result.Cmd == nil {
		t.Fatal("operation did not emit a service request")
	}
	request, ok := result.Cmd().(CapabilityRequestMsg)
	if !ok {
		t.Fatalf("unexpected operation message %T", result.Cmd())
	}
	return request
}

func capabilityAction(catalog *capabilityCatalog, operation string) ui.EventResult {
	return catalog.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.capability." + operation})
}

func completeCapability(model *Model, catalog *capabilityCatalog, request CapabilityRequestMsg, content string) {
	model.applyCapabilityResult(CapabilityResultMsg{Target: model, Sequence: request.Sequence, Snapshot: model.capabilitySnapshot, Content: content})
	catalog.consumeResult()
}

func TestSkillDetailPagesBoundPreviewAndRetainCatalog(t *testing.T) {
	model, root, host := capabilityFixture(t, captypes.KindSkill)
	catalog := root.capCatalog
	catalog.filter.SetValue("guide")
	catalog.views[catalog.viewKey()] = capabilityListState{query: "guide", selected: "vendor:guide"}
	catalog.syncRows()
	capabilityAction(catalog, "inspect")
	if model.capabilityPending != nil {
		t.Fatal("inspection started I/O")
	}
	catalog.detailForm("instructions")
	request := capabilityRequest(t, capabilityAction(catalog, "read-file"))
	if request.Action.Content != "SKILL.md" || request.Action.ID != "vendor:guide" {
		t.Fatalf("incorrect skill read: %+v", request.Action)
	}
	completeCapability(model, catalog, request, strings.Repeat("界", capabilityPreviewLimit))
	preview := catalog.forms[catalog.viewKey()].Field("preview")
	if !preview.ReadOnly || !utf8.ValidString(preview.Value.String()) || len(preview.Value.String()) > capabilityPreviewLimit+60 || !strings.Contains(preview.Value.String(), "truncated") {
		t.Fatal("preview is not bounded read-only UTF-8")
	}
	for _, page := range []string{"overview", "instructions", "files", "access"} {
		catalog.detailForm(page)
		if catalog.tabs.ActiveID() != page {
			t.Fatalf("detail tab lost %s", page)
		}
	}
	catalog.detailForm("files")
	request = capabilityRequest(t, capabilityAction(catalog, "list-files"))
	completeCapability(model, catalog, request, `["SKILL.md","references/notes.md"]`)
	if len(catalog.tree.Items()) != 2 {
		t.Fatal("file tree was not populated by the service listing")
	}
	catalog.tree.SelectIndex(1)
	catalog.selectFile()
	if got := catalog.forms[catalog.viewKey()].Field("file").Value.String(); got != "references/notes.md" {
		t.Fatalf("selected file=%q", got)
	}
	catalog.back()
	if catalog.detail || catalog.filter.Value() != "guide" {
		t.Fatal("Back lost retained catalog query")
	}
	host.MarkLayout()
	host.Layout()
}

func TestSkillManualModeAndManagedCopyUseExactIdentity(t *testing.T) {
	model, root, _ := capabilityFixture(t, captypes.KindSkill)
	catalog := root.capCatalog
	capabilityAction(catalog, "inspect")
	catalog.detailForm("access")
	model.capabilityDraft.SetValue("mode", ui.StringValue("manual"))
	if !catalog.dirty() {
		t.Fatal("invocation mode has no dirty guard")
	}
	request := capabilityRequest(t, capabilityAction(catalog, "save-mode"))
	if request.Action.Skill == nil || request.Action.Skill.Mode != "manual" || !request.Action.Skill.Managed || request.Action.Skill.Digest != "digest-1" {
		t.Fatal("mode save dropped immutable artifact identity")
	}
	completeCapability(model, catalog, request, "")
	if catalog.dirty() {
		t.Fatal("acknowledged mode remains dirty")
	}
	for _, bar := range catalog.actionBars() {
		if slices.Contains(bar.ButtonLabels(), "Use in session") {
			t.Fatal("removed skill invocation remains available")
		}
	}
	capabilityAction(catalog, "make-local-copy")
	for id, value := range map[string]string{"id": "local:guide-copy", "name": "guide-copy", "path": "skills/guide-copy"} {
		model.capabilityDraft.SetValue(id, ui.StringValue(value))
	}
	request = capabilityRequest(t, capabilityAction(catalog, "save"))
	if request.Action.Operation != "make-local-copy" || request.Action.ID != "vendor:guide" || request.Action.Skill.ID != "local:guide-copy" || request.Action.Skill.Managed {
		t.Fatalf("unsafe copy contract: %+v", request.Action)
	}
}

func TestSourceRemovalAndResetAreConfirmedAndPreimageBound(t *testing.T) {
	model, root, host := capabilityFixture(t, captypes.KindSkill)
	model.SetScope(types.SettingsScopeWorkspace)
	model.SelectCategory(catSources)
	root.capCatalog.switchKind("source")
	host.MarkLayout()
	host.Layout()
	catalog := root.capCatalog
	for _, operation := range []string{"remove-source", "reset-inherited"} {
		result := capabilityAction(catalog, operation)
		prompt, ok := result.Cmd().(PromptMsg)
		if !ok || prompt.CapabilityAction.Operation != operation || !root.PromptRelevant(prompt) {
			t.Fatalf("missing source confirmation for %s", operation)
		}
		before := model.capabilities.Sources["vendor"]
		changed := before
		changed.Location = "https://example.test/changed"
		model.capabilities.Sources["vendor"] = changed
		if root.PromptRelevant(prompt) {
			t.Fatal("old confirmation survived changed entity preimage")
		}
		model.capabilities.Sources["vendor"] = before
	}
}

func TestMCPGrantRequiresExplicitEffectScopeAndCurrentFingerprint(t *testing.T) {
	model, root, _ := capabilityFixture(t, captypes.KindMCP)
	catalog := root.capCatalog
	capabilityAction(catalog, "inspect")
	catalog.detailForm("tools")
	capabilityAction(catalog, "inspect-item")
	if !strings.Contains(catalog.forms[catalog.viewKey()].Field("schema").Value.String(), "lookup-schema") {
		t.Fatal("tool inspection omitted exact schema identity")
	}
	capabilityAction(catalog, "review-grant")
	form := model.capabilityDraft
	form.SetValue("grant-enabled", ui.BoolValue(true))
	if _, err := catalog.draftAction(); err == nil {
		t.Fatal("grant inferred a missing effect/scope")
	}
	form.SetValue("grant-effect", ui.StringValue("read"))
	form.SetValue("grant-scope", ui.StringValue("workspace/docs"))
	request := capabilityRequest(t, capabilityAction(catalog, "save"))
	grant := request.Action.Connection.Grants["lookup"]
	if request.Action.Operation != "save-connection" || grant.Fingerprint != "lookup-schema" || grant.Effect != captypes.EffectRead || grant.Scope != "workspace/docs" || grant.Disabled {
		t.Fatalf("grant lost explicit envelope: %+v", grant)
	}
	if _, exists := request.Action.Connection.Grants["existing"]; !exists {
		t.Fatal("grant save dropped other tools")
	}
	completeCapability(model, catalog, request, "")
	model.capabilitySnapshot.Connections[0].Tools[0].Fingerprint = "changed-schema"
	if _, err := catalog.draftAction(); err == nil {
		t.Fatal("changed fingerprint accepted the old access draft")
	}
}

func TestMCPPromptResourcePreviewRequiresExplicitCurrentStage(t *testing.T) {
	model, root, _ := capabilityFixture(t, captypes.KindMCP)
	catalog := root.capCatalog
	capabilityAction(catalog, "inspect")
	catalog.detailForm("prompts")
	capabilityAction(catalog, "inspect-item")
	if capabilityAction(catalog, "prompt").Cmd != nil {
		t.Fatal("missing required prompt argument was fetched")
	}
	form := catalog.forms[catalog.viewKey()]
	form.SetValue("argument/topic", ui.StringValue("retained drafts"))
	request := capabilityRequest(t, capabilityAction(catalog, "prompt"))
	if request.Action.Content != "explain" || request.Action.Arguments["topic"] != "retained drafts" {
		t.Fatal("prompt arguments lost exact identity")
	}
	completeCapability(model, catalog, request, `{"Content":[{"Type":"prompt","Text":"assistant: explanation"}]}`)
	if !strings.Contains(form.Field("preview").Value.String(), "explanation") {
		t.Fatal("prompt result was not rendered")
	}
	request = capabilityRequest(t, capabilityAction(catalog, "stage-context"))
	if request.Action.Arguments["source"] != "explain" || request.Action.Arguments["operation"] != "prompt" {
		t.Fatal("context stage omitted source attribution")
	}
	completeCapability(model, catalog, request, "staged")
	form.SetValue("argument/topic", ui.StringValue("new topic"))
	if capabilityAction(catalog, "stage-context").Cmd != nil {
		t.Fatal("old preview staged after argument edit")
	}
	catalog.detailForm("resources")
	capabilityAction(catalog, "inspect-item")
	request = capabilityRequest(t, capabilityAction(catalog, "resource"))
	completeCapability(model, catalog, request, `{"Content":[{"Type":"resource","Text":"failed"}],"IsError":true}`)
	if capabilityAction(catalog, "stage-context").Cmd != nil {
		t.Fatal("failed resource response staged context")
	}
}

func TestMCPPreviewRejectsLateChangedInputsAndDetachedDraftTest(t *testing.T) {
	model, root, _ := capabilityFixture(t, captypes.KindMCP)
	catalog := root.capCatalog
	capabilityAction(catalog, "inspect")
	catalog.detailForm("resources")
	capabilityAction(catalog, "inspect-item")
	request := capabilityRequest(t, capabilityAction(catalog, "resource"))
	form := catalog.forms[catalog.viewKey()]
	form.SetValue("uri", ui.StringValue("docs://other"))
	completeCapability(model, catalog, request, `{"Content":[{"Text":"old resource"}]}`)
	if strings.Contains(form.Field("preview").Value.String(), "old resource") {
		t.Fatal("late resource overwrote newer URI")
	}
	capabilityAction(catalog, "edit")
	model.capabilityDraft.SetValue("name", ui.StringValue("Edited docs"))
	request = capabilityRequest(t, capabilityAction(catalog, "test"))
	if request.Action.Connection.Name != "Edited docs" || model.capabilities.Connections["docs"].Name != "Documentation" {
		t.Fatal("detached test mutated saved definition")
	}
	model.capabilityDraft.SetValue("name", ui.StringValue("Newer docs"))
	completeCapability(model, catalog, request, "detached result")
	if !strings.Contains(catalog.message, "older draft") || !catalog.dirty() {
		t.Fatal("old detached test certified newer edits")
	}
}

func TestMCPAuthorizationReturnAndCancelKeepOperationOwnership(t *testing.T) {
	model, root, _ := capabilityFixture(t, captypes.KindMCP)
	catalog := root.capCatalog
	capabilityAction(catalog, "inspect")
	catalog.detailForm("connection")
	signIn := capabilityRequest(t, capabilityAction(catalog, "sign-in"))
	form := catalog.forms[catalog.viewKey()]
	form.SetValue("auth-return", ui.StringValue("http://localhost/return?code=private&state=exact"))
	callback := capabilityRequest(t, capabilityAction(catalog, "auth-return"))
	if callback.Action.Content != form.Field("auth-return").Value.String() || model.capabilitySequence != signIn.Sequence {
		t.Fatal("auth callback replaced the sign-in owner")
	}
	completeCapability(model, catalog, callback, "")
	if model.capabilityPending == nil || form.Field("auth-return").Value.String() != "" {
		t.Fatal("auth return did not preserve pending sign-in or clear private callback")
	}
	cancel := capabilityRequest(t, capabilityAction(catalog, "cancel"))
	if cancel.Action.Arguments["sequence"] != fmt.Sprint(signIn.Sequence) || cancel.Action.ID != "docs" {
		t.Fatal("cancel lost original operation identity")
	}
	completeCapability(model, catalog, cancel, "cancelled")
	model.applyCapabilityResult(CapabilityResultMsg{Target: model, Sequence: signIn.Sequence, Content: "late sign-in"})
	if model.capabilityResult.Sequence != cancel.Sequence {
		t.Fatal("cancelled operation overwrote current result")
	}
	request := capabilityRequest(t, capabilityAction(catalog, "sign-out"))
	if request.Action.Operation != "sign-out" {
		t.Fatal("sign-out is not a service operation")
	}
}

func TestCapabilityDirtyScopeAndDeepLinkRetainDraftUntilDiscard(t *testing.T) {
	model, root, _ := capabilityFixture(t, captypes.KindSkill)
	catalog := root.capCatalog
	capabilityAction(catalog, "add-local")
	model.capabilityDraft.SetValue("name", ui.StringValue("changed"))
	result := root.OpenCapabilities(captypes.KindMCP, "docs", "docs")
	prompt := result.Cmd().(PromptMsg)
	if !root.PromptRelevant(prompt) || model.SelectedCategory() != catSkills || model.capabilityDraft == nil {
		t.Fatal("deep link silently discarded a skill draft")
	}
	result = root.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.confirm", Payload: PromptDiscardEditor})
	if model.SelectedCategory() != catMCP || catalog.id != "docs" || model.capabilityDraft != nil {
		t.Fatal("confirmed deep link did not navigate")
	}
	completeCapability(model, catalog, capabilityRequest(t, result), "")
	capabilityAction(catalog, "edit")
	model.capabilityDraft.SetValue("name", ui.StringValue("workspace edits"))
	result = root.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.scope", Payload: scopeTabWorkspace})
	if prompt, ok := result.Cmd().(PromptMsg); !ok || prompt.Kind != PromptDiscardEditor || model.Scope() != types.SettingsScopeApplication {
		t.Fatal("scope switch bypassed capability draft guard")
	}
	result = root.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.confirm", Payload: PromptDiscardEditor})
	if message, ok := result.Cmd().(ScopeMsg); !ok || message.Scope != types.SettingsScopeWorkspace {
		t.Fatal("discard did not resume requested scope transition")
	}
}

func TestCapabilityNativePagesStayBoundedAtNarrowWidths(t *testing.T) {
	for _, size := range []ui.Size{{W: 80, H: 24}, {W: 130, H: 36}} {
		model, root, host := capabilityFixture(t, captypes.KindMCP)
		catalog := root.capCatalog
		capabilityAction(catalog, "inspect")
		for _, page := range []string{"overview", "connection", "tools", "resources", "prompts", "access", "activity"} {
			catalog.detailForm(page)
			host.Resize(size.W, size.H)
			host.MarkLayout()
			host.Layout()
			bounds := catalog.Bounds()
			// The decision bar lives on the settings footer row.
			if bar := catalog.bar.Bounds(); bounds.Y+bar.Y != root.Bounds().H-1 || bounds.X+bar.X+bar.W+root.tokens.ButtonGap() != root.closeBar.Bounds().X {
				t.Fatalf("%s at %+v: decisions are not on the footer row: %+v", page, size, bar)
			}
			for _, child := range catalog.Children() {
				rect := child.Bounds()
				if rect.Empty() || child == catalog.bar {
					continue
				}
				if rect.X < 0 || rect.Y < 0 || rect.X+rect.W > bounds.W || rect.Y+rect.H > bounds.H {
					t.Fatalf("%s at %+v: %s escapes %+v with %+v", page, size, child.ID(), bounds, rect)
				}
				if rect.Y <= catalog.statusY && rect.Y+rect.H > catalog.statusY {
					t.Fatalf("%s overlays status row", child.ID())
				}
			}
			buffer := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buffer)
			if !strings.Contains(bufferPlain(buffer), "MCP") {
				t.Fatal("retained MCP frame did not paint")
			}
		}
		if model.capabilityPending != nil {
			t.Fatal("painting details initiated an operation")
		}
	}
}

func TestCapabilityCatalogInstalledDiscoverOperationsAndScroll(t *testing.T) {
	model, root, host := capabilityFixture(t, captypes.KindSkill)
	catalog := root.capCatalog
	for _, operation := range []string{"disable", "enable", "update"} {
		request := capabilityRequest(t, capabilityAction(catalog, operation))
		want := operation
		if operation == "update" {
			want = "preview-update"
		}
		if request.Action.Operation != want || request.Action.ID != "vendor:guide" {
			t.Fatalf("catalog operation changed identity: %+v", request.Action)
		}
		completeCapability(model, catalog, request, "")
	}
	for index := 0; index < 40; index++ {
		model.capabilitySnapshot.Entries = append(model.capabilitySnapshot.Entries, captypes.Entry{ID: fmt.Sprintf("local:%02d", index), Name: fmt.Sprintf("Local %02d", index), Kind: captypes.KindSkill, Installed: true})
	}
	model.capabilityRevision++
	host.MarkLayout()
	host.Layout()
	catalog.store.SelectIndex(30)
	catalog.store.Scroll().ScrollTo(20)
	row, _ := catalog.store.Selected()
	offset := catalog.store.Scroll().Offset()
	capabilityAction(catalog, "inspect")
	catalog.back()
	host.MarkLayout()
	host.Layout()
	selected, _ := catalog.store.Selected()
	if selected.ID != row.ID || catalog.store.Scroll().Offset() != offset {
		t.Fatal("detail navigation or layout clamped retained store scroll")
	}
	catalog.filter.SetValue("new")
	catalog.views[catalog.viewKey()] = capabilityListState{query: "new"}
	request := capabilityRequest(t, capabilityAction(catalog, "search"))
	model.applyCapabilityResult(CapabilityResultMsg{Target: model, Sequence: request.Sequence, Entries: []captypes.Entry{{ID: "vendor:new", Kind: captypes.KindSkill, Name: "New guide", Version: "pinned", Source: "vendor"}}})
	catalog.consumeResult()
	catalog.syncRows()
	request = capabilityRequest(t, capabilityAction(catalog, "install"))
	if request.Action.Operation != "preview-install" || request.Action.ID != "vendor:new" || request.Action.Scope != captypes.ScopeApplication {
		t.Fatal("Discover install lost explicit identity/scope")
	}
	preview := captypes.AcquisitionPreview{ID: "vendor:new", Kind: captypes.KindSkill, Scope: captypes.ScopeApplication, Operation: "install", Candidate: captypes.Entry{ID: "vendor:new", Version: "pinned"}, Token: "exact-preimage"}
	encoded, _ := json.Marshal(preview)
	completeCapability(model, catalog, request, string(encoded))
	if catalog.detailPage != "acquisition" || !strings.Contains(catalog.forms[catalog.viewKey()].Field("acquisition").Value.String(), "pinned") {
		t.Fatal("Install skipped the exact revision preview")
	}
	request = capabilityRequest(t, capabilityAction(catalog, "confirm-acquisition"))
	if request.Action.Preview == nil || request.Action.Preview.Token != preview.Token || request.Action.Operation != "install" {
		t.Fatal("Confirmation dropped the exact preview precondition")
	}
}

func TestCapabilityReaderUsesNativeReadOnlyScrollAndCopyTarget(t *testing.T) {
	model, root, host := capabilityFixture(t, captypes.KindSkill)
	catalog := root.capCatalog
	capabilityAction(catalog, "inspect")
	catalog.detailForm("instructions")
	request := capabilityRequest(t, capabilityAction(catalog, "read-file"))
	completeCapability(model, catalog, request, strings.Repeat("instruction row\n", 100))
	host.MarkLayout()
	host.Layout()
	host.Focus(catalog.reader.ID())
	before := catalog.reader.Value()
	host.Handle(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if catalog.reader.Value() != before {
		t.Fatal("preview reader modified managed instructions")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyPgDown})
	host.Handle(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if catalog.reader.ScrollOffset() == 0 {
		t.Fatal("long instruction result cannot be scrolled")
	}
	catalog.reader.SetSelection(0, 11)
	if selected, ok := catalog.reader.TextSelection(); !ok || selected == "" {
		t.Fatal("preview lacks native selection/copy target")
	}
}

func TestCapabilitySnapshotRefreshDoesNotCompleteSignIn(t *testing.T) {
	model, root, _ := capabilityFixture(t, captypes.KindMCP)
	catalog := root.capCatalog
	capabilityAction(catalog, "inspect")
	catalog.detailForm("connection")
	signIn := capabilityRequest(t, capabilityAction(catalog, "sign-in"))
	refresh := capabilityRequest(t, capabilityAction(catalog, "snapshot"))
	snapshot := model.capabilitySnapshot
	snapshot.Generation++
	snapshot.Connections[0].AuthPending = true
	snapshot.Connections[0].AuthURL = "https://example.test/signin"
	model.applyCapabilityResult(CapabilityResultMsg{Target: model, Sequence: refresh.Sequence, Snapshot: snapshot})
	if model.capabilityPending == nil || model.capabilitySequence != signIn.Sequence {
		t.Fatal("status refresh stole pending sign-in ownership")
	}
	catalog.syncDetailRows()
	if got := catalog.forms[catalog.viewKey()].Field("auth-url").Value.String(); got != "https://example.test/signin" {
		t.Fatal("pending authorization URL did not arrive in retained connection page")
	}
}

func TestCapabilityRetainedSelectorsSurviveLayoutAndSnapshot(t *testing.T) {
	model, root, host := capabilityFixture(t, captypes.KindMCP)
	catalog := root.capCatalog
	capabilityAction(catalog, "inspect")
	catalog.detailForm("prompts")
	capabilityAction(catalog, "inspect-item")
	host.MarkLayout()
	host.Layout()
	control := catalog.form.Field("argument/topic")
	form := catalog.inputForms[catalog.viewKey()]
	catalog.forms[catalog.viewKey()].SetValue("argument/topic", ui.StringValue("keep this text"))
	host.MarkLayout()
	host.Layout()
	if catalog.form.Field("argument/topic") != control || catalog.inputForms[catalog.viewKey()] != form {
		t.Fatal("layout rebuilt a retained prompt argument editor")
	}
	model.SelectCategory(catAgents)
	root.agents.begin("role/researcher", false)
	root.agents.setPage("tools")
	host.MarkLayout()
	host.Layout()
	host.Focus(root.agents.form.ID())
	root.agents.form.FocusField("agent-tool-mode")
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	selector := root.agents.form.Field("agent-tool-mode")
	if !selector.SelectOpen() {
		t.Fatal("tool access selector did not open")
	}
	snapshot := model.capabilitySnapshot
	snapshot.Generation++
	model.SetCapabilitySnapshot(snapshot)
	root.agents.syncModel()
	if root.agents.form.Field("agent-tool-mode") != selector {
		t.Fatal("unchanged capability snapshot rebuilt agent tool selectors")
	}
	if !selector.SelectOpen() {
		t.Fatal("capability snapshot closed a retained tool selector")
	}
}

func TestCapabilityApplicationScopeNeverImportsWorkspaceDefinitions(t *testing.T) {
	model, _, _ := capabilityFixture(t, captypes.KindSkill)
	snapshot := model.capabilitySnapshot
	snapshot.Generation++
	snapshot.Settings = captypes.Clone(snapshot.Settings)
	snapshot.Settings.Connections["workspace-only"] = captypes.ConnectionConfig{ID: "workspace-only", Name: "Workspace", Scope: captypes.ScopeWorkspace}
	skill := snapshot.Settings.Skills["vendor:guide"]
	skill.Mode, skill.Scope = "disabled", captypes.ScopeWorkspace
	snapshot.Settings.Skills["vendor:guide"] = skill
	model.SetCapabilitySnapshot(snapshot)
	if _, exists := model.AppSettings().Capabilities.Connections["workspace-only"]; exists {
		t.Fatal("application preferences imported a workspace-only connection")
	}
	if model.capabilities.Skills["vendor:guide"].Mode != "automatic" {
		t.Fatal("workspace override replaced application definition")
	}
}

func TestCapabilityDeepLinkCanFocusRetainedHiddenCatalog(t *testing.T) {
	model, root, host := capabilityFixture(t, captypes.KindSkill)
	model.SelectCategory(catAppearance)
	host.MarkLayout()
	host.Layout()
	root.OpenCapabilities(captypes.KindMCP, "", "docs")
	if host.Focused() != root.capCatalog.reader.ID() {
		t.Fatal("deep link could not focus the newly visible retained capability owner")
	}
}

func TestCapabilityManagerEscapeReturnsNormalSettingsClose(t *testing.T) {
	_, root, host := capabilityFixture(t, captypes.KindSkill)
	host.Focus(root.capCatalog.filter.ID())
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if result.Cmd == nil {
		t.Fatal("empty capability manager consumed Escape without closing")
	}
	if _, ok := result.Cmd().(CancelMsg); !ok {
		t.Fatal("manager Escape bypassed Settings close ownership")
	}
}

func webSearchFixture(t *testing.T) (*Model, *WorkspaceSettingsSurface, *ui.Host, captypes.Snapshot) {
	t.Helper()
	model, root, host := capabilityFixture(t, captypes.KindMCP)
	snapshot := model.capabilitySnapshot
	snapshot.Generation++
	snapshot.Settings = captypes.WithDefaults(snapshot.Settings)
	web := snapshot.Settings.Connections[captypes.WebConnectionID]
	snapshot.Entries = append(snapshot.Entries, captypes.Entry{ID: web.ID, Kind: captypes.KindMCP, Name: web.Name, Transport: web.Transport, Installed: true, Origin: captypes.ScopeApplication})
	model.SetCapabilitySnapshot(snapshot)
	// Support comes from each account's protocol declaration. The Responses
	// account is a gateway, not one vendor host.
	model.accounts = map[string]provider.Account{
		"example-account": {ID: "example-account", ProviderID: "ollama-cloud", Label: "Ollama Fixture", APIKey: "not-a-real-key",
			Connection: provider.Template{ProviderURL: "https://ollama.example/api", APIStandard: "ollama"}},
		"gateway": {ID: "gateway", ProviderID: "custom", Label: "Gateway Responses", APIKey: "not-a-real-key",
			Connection: provider.Template{ProviderURL: "https://gateway.example/v1", APIStandard: "openai-responses", AuthType: provider.AuthProtocol}},
		"chat": {ID: "chat", ProviderID: "custom", Label: "Chat Only", APIKey: "not-a-real-key",
			Connection: provider.Template{ProviderURL: "https://chat.example/v1", APIStandard: "openai"}},
	}
	return model, root, host, snapshot
}

func TestWebSearchMethodsChooseOnAccountAndModelExplicitly(t *testing.T) {
	model, root, host, snapshot := webSearchFixture(t)
	request := capabilityRequest(t, root.OpenCapabilities(captypes.KindMCP, "", captypes.WebConnectionID))
	completeCapability(model, root.capCatalog, request, "")
	catalog := root.capCatalog
	paint := func() *surface.CellBuffer { return paintCapabilityStore(host, 130, 36) }
	click := func(buffer *surface.CellBuffer, label string) ui.EventResult {
		t.Helper()
		x, y := storeTextPoint(t, buffer, label)
		return host.Handle(tea.MouseClickMsg{X: x + 1, Y: y, Button: tea.MouseLeft})
	}
	key := func(code rune) ui.EventResult { return host.Handle(tea.KeyPressMsg{Code: code}) }
	apply := func(request CapabilityRequestMsg, next captypes.Snapshot, content string) {
		t.Helper()
		model.applyCapabilityResult(CapabilityResultMsg{Target: model, Sequence: request.Sequence, Snapshot: next, Content: content})
		catalog.consumeResult()
	}

	buffer := paint()
	if got := bufferPlain(buffer); !strings.Contains(got, "Search methods  Free engines and provider keys") || strings.Contains(got, "fallback") {
		t.Fatalf("overview does not name the search methods:\n%s", got)
	}
	if result := click(buffer, "[ Search methods ]"); result.Cmd != nil {
		t.Fatal("opening Search methods made a request")
	}
	if got := bufferPlain(paint()); !strings.Contains(got, "There are no account methods") || !strings.Contains(got, "[ Add method ]") {
		t.Fatalf("empty Search methods page is unclear:\n%s", got)
	}
	if result := click(paint(), "[ Add method ]"); result.Cmd != nil {
		t.Fatal("a new method without an account made a request")
	}
	form := model.capabilityDraft
	if catalog.edit != webRouteEditor || form == nil || !ui.BoolFieldValue(form.Field(webRouteOn)) ||
		form.Field(webRouteAccount).Value.String() != "" || !form.Field(webRouteModel).Disabled {
		t.Fatal("Add method did not open an explicit On, Account and Model editor")
	}
	if got := bufferPlain(paint()); !strings.Contains(got, "Use this method") || !strings.Contains(got, "Account") ||
		!strings.Contains(got, "Model") || !strings.Contains(got, "Search method") {
		t.Fatalf("editor hides a required choice:\n%s", got)
	}
	if host.Focused() != catalog.form.ID() {
		t.Fatal("the editor did not take keyboard focus")
	}

	// The account list comes from protocol declarations only.
	key(tea.KeyEnter)
	buffer = paint()
	if got := bufferPlain(buffer); !strings.Contains(got, "Ollama Fixture") || !strings.Contains(got, "Gateway Responses") || strings.Contains(got, "Chat Only") {
		t.Fatalf("account picker offered the wrong accounts:\n%s", got)
	}
	if result := click(buffer, "Ollama Fixture"); result.Cmd != nil {
		t.Fatal("an account that searches without a model requested a model list")
	}
	if form.Field(webRouteAccount).Value.String() != "example-account" || !form.Field(webRouteModel).Disabled || form.Field(webRouteModel).Value.String() != "" {
		t.Fatalf("choosing the Ollama account did not mark the model as not used: %+v", form.Field(webRouteModel))
	}
	if got := bufferPlain(paint()); !strings.Contains(got, "Not used") || !strings.Contains(got, "Ollama searches with the account") {
		t.Fatalf("editor does not explain the model choice:\n%s", got)
	}

	request = capabilityRequest(t, click(paint(), "[ Test ]"))
	if action := request.Action; action.Operation != captypes.TestWebSearchRoute || action.Scope != captypes.ScopeApplication ||
		action.WebSearchRoute == nil || action.WebSearchRoute.Account != "example-account" || action.WebSearchRoute.Model != "" {
		t.Fatalf("Test sent the wrong route: %+v", action)
	}
	apply(request, snapshot, "Search works: 5 sources in 1.4 s.")
	if got := bufferPlain(paint()); !strings.Contains(got, "Search works: 5 sources in 1.4 s.") || !catalog.dirty() {
		t.Fatalf("Test result is missing or Test saved the draft:\n%s", got)
	}

	request = capabilityRequest(t, click(paint(), "[ Save method ]"))
	saved := request.Action.WebSearchRoute
	if action := request.Action; action.Operation != captypes.SaveWebSearchRoute || action.Scope != captypes.ScopeApplication || saved == nil ||
		saved.ID == "" || saved.Account != "example-account" || saved.Model != "" || saved.Disabled || action.Connection != nil || len(action.Arguments) != 0 {
		t.Fatalf("Save sent more or less than one route: %+v", action)
	}
	snapshot.Generation++
	snapshot.Settings.WebSearchRoutes = []captypes.WebSearchRoute{*saved}
	snapshot.WebSearch = map[string]captypes.WebSearchObservation{saved.ID: {Working: true, Results: 5, LatencyMS: 1420, At: 1}}
	apply(request, snapshot, "")
	if catalog.edit != "" || catalog.detailPage != webSearchPage {
		t.Fatal("a saved method did not return to the Search methods list")
	}
	if got := bufferPlain(paint()); !strings.Contains(got, "Search method saved.") || !strings.Contains(got, "Ollama Fixture") ||
		!strings.Contains(got, "On · Working · 1.4 s") {
		t.Fatalf("list does not show the saved method and its last result:\n%s", got)
	}

	// A second method chooses a model from the account's own model list.
	click(paint(), "[ Add method ]")
	form = model.capabilityDraft
	key(tea.KeyEnter)
	chosen := click(paint(), "Gateway Responses")
	if chosen.Cmd == nil {
		t.Fatal("a model search account did not request its model list")
	}
	load, ok := chosen.Cmd().(FetchModelsMsg)
	if !ok || load.AccountID != "gateway" || model.capabilityPending != nil {
		t.Fatalf("choosing the account made something other than a model-list request: %+v", load)
	}
	if form.Field(webRouteModel).Disabled {
		t.Fatal("the model choice stayed off for a protocol that needs a model")
	}
	key(tea.KeyTab)
	key(tea.KeyEnter)
	model.Update(ModelsMsg{ProviderID: load.ProviderID, AccountID: load.AccountID, Generation: load.Generation, Models: []types.ModelEntry{{ID: "search-model-1"}}})
	if got := bufferPlain(paint()); !strings.Contains(got, "search-model-1") {
		t.Fatalf("model list did not reach the open picker:\n%s", got)
	}
	key(tea.KeyEnter)
	if got := form.Field(webRouteModel).Value.String(); got != "search-model-1" {
		t.Fatalf("selecting the displayed model chose %q", got)
	}
	// The On switch is a normal field: turn this method off before saving.
	catalog.form.FocusField(webRouteOn)
	key(tea.KeySpace)
	if ui.BoolFieldValue(form.Field(webRouteOn)) {
		t.Fatal("the On switch did not turn the method off")
	}
	request = capabilityRequest(t, click(paint(), "[ Save method ]"))
	second := request.Action.WebSearchRoute
	if second == nil || second.ID == saved.ID || second.Account != "gateway" || second.Model != "search-model-1" || !second.Disabled {
		t.Fatalf("second method saved the wrong route: %+v", second)
	}
	snapshot.Generation++
	snapshot.Settings.WebSearchRoutes = append(snapshot.Settings.WebSearchRoutes, *second)
	apply(request, snapshot, "")
	if got := bufferPlain(paint()); !strings.Contains(got, "search-model-1") || !strings.Contains(got, "Off") {
		t.Fatalf("list does not show the second method as off:\n%s", got)
	}

	// Remove asks first and sends only the selected route.
	result := click(paint(), "[ Remove ]")
	prompt, ok := result.Cmd().(PromptMsg)
	if !ok || prompt.Kind != PromptRemoveSearchMethod || prompt.CapabilityAction == nil || !root.PromptRelevant(prompt) {
		t.Fatalf("Remove did not ask for confirmation: %+v", prompt)
	}
	request = capabilityRequest(t, catalog.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.capability.confirm-remove-route", Payload: *prompt.CapabilityAction}))
	if action := request.Action; action.Operation != captypes.RemoveWebSearchRoute || action.WebSearchRoute.ID != prompt.ID {
		t.Fatalf("confirmed removal sent the wrong route: %+v", action)
	}
}

func TestWebSearchMethodsAreReadOnlyInWorkspaceScope(t *testing.T) {
	model, root, host, snapshot := webSearchFixture(t)
	snapshot.Settings.WebSearchRoutes = []captypes.WebSearchRoute{{ID: "r1", Account: "example-account"}}
	snapshot.WebSearch = map[string]captypes.WebSearchObservation{"r1": {Detail: "The account refused the key (HTTP 401). Check the account in Settings.", At: 1}}
	model.SetCapabilitySnapshot(snapshot)
	model.SetScope(types.SettingsScopeWorkspace)
	request := capabilityRequest(t, root.OpenWebSearchMethods())
	if request.Action.Operation != "snapshot" {
		t.Fatalf("opening Search methods did more than read the snapshot: %+v", request.Action)
	}
	completeCapability(model, root.capCatalog, request, "")
	got := bufferPlain(paintCapabilityStore(host, 130, 36))
	if strings.Contains(got, "[ Add method ]") || strings.Contains(got, "[ Remove ]") || !strings.Contains(got, "Switch to Application") ||
		!strings.Contains(got, "On · Failed") || !strings.Contains(got, "The account refused the key") {
		t.Fatalf("workspace scope could edit search methods or hid the failure:\n%s", got)
	}
}

func TestWebSearchMethodsDeepLinkRetainsDirtyAgentEditor(t *testing.T) {
	model, root, _ := capabilityFixture(t, captypes.KindMCP)
	snapshot := model.capabilitySnapshot
	snapshot.Generation++
	snapshot.Settings = captypes.WithDefaults(snapshot.Settings)
	model.SetCapabilitySnapshot(snapshot)
	root.ShowAgentPage("role/default", "Model")
	draft := root.agents.draft
	if draft == nil {
		t.Fatal("agent editor did not open")
	}
	draft.SetValue(fieldModel, ui.StringValue("unsaved-agent-model"))
	root.OpenWebSearchMethods()
	if root.agents.draft != draft || !root.agents.dirty() || root.agents.confirm != "discard" || model.SelectedCategory() != catAgents {
		t.Fatal("deep link discarded or replaced an unsaved agent draft")
	}
	root.agents.HandleAction(ui.EventContext{}, ui.Action{ID: "agents.discard"})
	if root.agents.draft != nil || root.capCatalog.detailPage != webSearchPage ||
		root.capCatalog.id != captypes.WebConnectionID || model.SelectedCategory() != catMCP {
		t.Fatalf("discard did not resume Search methods: draft=%t page=%q id=%q category=%d",
			root.agents.draft != nil, root.capCatalog.detailPage, root.capCatalog.id, model.SelectedCategory())
	}
}

func TestWebSearchRoutesPersistAsOneApplicationList(t *testing.T) {
	route := captypes.WebSearchRoute{ID: "r1", Account: "original-account", Model: "original-model"}
	before := types.AppSettings{Capabilities: captypes.WithDefaults(captypes.Settings{WebSearchRoutes: []captypes.WebSearchRoute{route}})}
	after := types.CloneAppSettings(before)
	after.Capabilities.WebSearchRoutes = []captypes.WebSearchRoute{route, {ID: "r2", Account: "ollama-account"}}
	edits := types.SettingsEdits(before, after)
	if len(edits) != 1 || !slices.Equal(edits[0].Path, []string{"capabilities", "web_search_routes"}) {
		t.Fatalf("Save could write unrelated or partial fields: %+v", edits)
	}
	latest := types.CloneAppSettings(before)
	peer := captypes.ConnectionConfig{ID: "docs", Name: "Peer MCP", Transport: "http", URL: "https://docs.example/mcp"}
	latest.Capabilities.Connections[peer.ID] = peer
	if err := types.ApplySettingsEdits(&latest, edits); err != nil {
		t.Fatal(err)
	}
	saved := captypes.Clone(latest.Capabilities)
	if !slices.Equal(saved.WebSearchRoutes, after.Capabilities.WebSearchRoutes) || !reflect.DeepEqual(saved.Connections[peer.ID], peer) {
		t.Fatalf("write changed the methods or a neighboring MCP: routes=%+v peer=%+v", saved.WebSearchRoutes, saved.Connections[peer.ID])
	}
	inherited := types.MergeAppWorkspace(latest, types.WorkspaceOverrides{
		Capabilities: json.RawMessage(`{"web_search_routes":[{"id":"w","account":"workspace-account"}],"connections":{"web":{"disabled":true}}}`),
	})
	if !slices.Equal(inherited.Capabilities.WebSearchRoutes, after.Capabilities.WebSearchRoutes) || !inherited.Capabilities.Connections["web"].Disabled {
		t.Fatal("workspace overrides added a search method or bypassed its own disable")
	}
	if patch := types.OverridesFromDiff(before, after).Capabilities; len(patch) != 0 {
		t.Fatalf("application-only methods leaked into workspace overrides: %s", patch)
	}
}

func TestAgentDynamicIntentAndSkillModesSurviveUnavailableSnapshots(t *testing.T) {
	model, root, _ := capabilityFixture(t, captypes.KindMCP)
	model.agents.Categories["researcher"] = types.CategorySettings{AllowedTools: []string{"mcp__docs__write", "mcp__missing__read"}, SkillModes: map[string]string{"vendor:guide": "manual"}}
	model.categories[catResearcher].Form = model.buildAgentForm(model.agents.Categories["researcher"], false)
	catalog := root.agents
	model.SelectCategory(catAgents)
	catalog.begin("role/researcher", false)
	catalog.setPage("tools")
	field := catalog.draft.Field("agent-tool/mcp__docs__write")
	if field == nil || !field.Value.Bool() || !field.Disabled || !strings.Contains(field.Description, "researcher") {
		t.Fatal("dynamic selected intent and read-only role reason are not separate")
	}
	if missing := catalog.draft.Field("agent-tool/mcp__missing__read"); missing == nil || !missing.Value.Bool() {
		t.Fatal("temporarily missing source selection disappeared")
	}
	catalog.draft.SetValue("agent-skill/vendor:guide", ui.StringValue("disabled"))
	model.SetCapabilitySnapshot(captypes.Snapshot{Generation: 2, Settings: model.capabilities})
	catalog.syncModel()
	if !field.Value.Bool() || !field.Disabled {
		t.Fatal("disconnection erased selected stable identity")
	}
	if !catalog.apply() {
		t.Fatal("valid capability intent did not apply")
	}
	configured := model.AppSettings().Agents.Categories["researcher"]
	if !slices.Contains(configured.AllowedTools, "mcp__docs__write") || !slices.Contains(configured.AllowedTools, "mcp__missing__read") || configured.SkillModes["vendor:guide"] != "disabled" {
		t.Fatalf("dynamic preferences did not round trip: %+v", configured)
	}
}

func TestBuiltinWebConnectionEditsWriteOnlyProviderKeys(t *testing.T) {
	model, root, host := capabilityFixture(t, captypes.KindMCP)
	settings := captypes.WithDefaults(model.capabilities)
	model.capabilities = settings
	snapshot := model.capabilitySnapshot
	snapshot.Settings, snapshot.StoredSecrets = settings, []string{"EXA_API_KEY"}
	model.SetCapabilitySnapshot(snapshot)
	catalog := root.capCatalog
	catalog.detail, catalog.id = true, captypes.WebConnectionID
	catalog.detailForm("connection")
	capabilityAction(catalog, "edit")
	form := model.capabilityDraft
	exa, brave := form.Field("secret/EXA_API_KEY"), form.Field("secret/BRAVE_SEARCH_API_KEY")
	if exa == nil || exa.Type != ui.FieldPassword || exa.Placeholder != "Stored" || brave == nil || brave.Placeholder != "Not set" {
		t.Fatalf("provider key fields: exa=%+v brave=%+v", exa, brave)
	}
	if form.Field("transport") != nil || form.Field("command") != nil {
		t.Fatal("a built-in connection must not offer transport or launch fields")
	}
	host.MarkLayout()
	host.Layout()
	buffer := surface.NewCellBuffer(130, 36)
	host.Paint(buffer)
	painted := bufferPlain(buffer)
	for _, want := range []string{"Exa API key", "Brave Search API key", "Stored", "Not set"} {
		if !strings.Contains(painted, want) {
			t.Fatalf("built-in editor did not paint %q:\n%s", want, painted)
		}
	}
	if _, err := catalog.draftAction(); err == nil {
		t.Fatal("saving an untouched key form must explain that nothing changed")
	}
	form.SetValue("secret/BRAVE_SEARCH_API_KEY", ui.StringValue("brave-value"))
	form.SetValue("secret/EXA_API_KEY", ui.StringValue("-"))
	action := capabilityRequest(t, capabilityAction(catalog, "save")).Action
	if action.Operation != "set-secrets" || action.Arguments["BRAVE_SEARCH_API_KEY"] != "brave-value" ||
		action.Arguments["EXA_API_KEY"] != "" || len(action.Arguments) != 2 || action.Connection != nil {
		t.Fatalf("key update action=%+v", action)
	}
}
