package settings

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

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
	request = capabilityRequest(t, capabilityAction(catalog, "use"))
	if request.Action.Operation != "use" || request.Action.ID != "vendor:guide" {
		t.Fatal("Use did not stage source-qualified identity")
	}
	completeCapability(model, catalog, request, "staged")
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
			for _, child := range catalog.Children() {
				rect := child.Bounds()
				if rect.Empty() {
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
