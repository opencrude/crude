package settings

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

type codeToolsStub struct {
	inventory                types.CodeToolInventory
	preview                  types.CodeToolRemovalPreview
	reads, previews, removes int
	unused                   bool
	ids                      []string
	err                      error
	removeErr                error
}

func (s *codeToolsStub) ReadCodeToolInventory(_ context.Context, offset, limit int) (types.CodeToolInventory, error) {
	s.reads++
	out := s.inventory
	out.HistoryOffset = offset
	return out, s.err
}
func (s *codeToolsStub) PreviewCodeToolRemoval(_ context.Context, ids []string, unused bool) (types.CodeToolRemovalPreview, error) {
	s.previews++
	s.ids, s.unused = ids, unused
	return s.preview, s.err
}
func (s *codeToolsStub) RemoveCodeTools(_ context.Context, preview types.CodeToolRemovalPreview) error {
	s.removes++
	if preview.Token != s.preview.Token {
		return errors.New("wrong preview")
	}
	if s.err == nil {
		s.inventory.Tools = nil
	}
	if s.removeErr != nil {
		return s.removeErr
	}
	return s.err
}

func codeToolsFixture(t *testing.T) (*Model, *WorkspaceSettingsSurface, *codeToolsStub) {
	t.Helper()
	tool := types.CodeTool{ID: "fixture/v1-digest", Package: "fixture", Version: "v1", Size: 2048, Dir: "C:/managed/fixture", Executable: "C:/managed/fixture/files/server",
		Source: "test registry", URL: "https://example.test/tool", SHA256: "reviewed-sha256", DigestOrigin: "first-download",
		Licenses: []string{"MIT"}, InstalledBy: "automatic with user approval", Approval: "review-123",
		Workspaces: []string{"C:/project"}, InstalledAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		FileEntries: []types.CodeToolFile{{Path: "files/server", Kind: "file", Size: 2048}, {Path: "manifest.json", Kind: "file", Size: 100}}}
	stub := &codeToolsStub{inventory: types.CodeToolInventory{Tools: []types.CodeTool{tool}, TotalBytes: 2048,
		Unmanaged: []types.UnmanagedCodeTool{{Name: "personal-server", Executable: "C:/user/bin/server", Source: "path"}},
		History:   []types.CodeToolHistoryEvent{{Kind: "install-failed", Package: "other", Detail: "download failed", By: "user"}}},
		preview: types.CodeToolRemovalPreview{Tools: []types.CodeTool{tool}, Paths: []string{tool.Dir}, Bytes: 2148, Token: "native-review"}}
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	m.SelectCategory(catCodeTools)
	s := NewSurface(theme.DefaultDark(), m)
	cmd := s.SetCodeTools(stub)
	if cmd == nil || stub.reads != 0 {
		t.Fatal("binding must defer I/O")
	}
	m.Update(cmd())
	s.SetModel(m)
	return m, s, stub
}

func applyCodeToolsCommand(t *testing.T, m *Model, s *WorkspaceSettingsSurface, action string) {
	t.Helper()
	result := s.HandleAction(ui.EventContext{}, ui.Action{ID: action})
	if result.Cmd == nil {
		t.Fatalf("missing command: %s", action)
	}
	m.Update(result.Cmd())
	s.SetModel(m)
}

func TestCodeToolsSettingsDetailsAndRemovalNeedExactNativeDecision(t *testing.T) {
	m, s, stub := codeToolsFixture(t)
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.code-tools.open/fixture/v1-digest"})
	for _, want := range []string{"test registry", "https://example.test/tool", "reviewed-sha256", "MIT", "review-123", "C:/project", "Installed:"} {
		if !strings.Contains(m.fieldAt(catCodeTools, "code-tools-overview").Value.String(), want) {
			t.Fatalf("accountability missing %q", want)
		}
	}
	if !strings.Contains(m.fieldAt(catCodeTools, "code-tools-files").Value.String(), "manifest.json") {
		t.Fatal("exact files absent")
	}
	for _, size := range []ui.Size{{W: 124, H: 30}, {W: 80, H: 24}} {
		host := ui.NewHost(theme.DefaultDark(), s)
		host.Resize(size.W, size.H)
		host.Layout()
		if !s.form.FocusField("code-tools-files") {
			t.Fatal("managed file list is not keyboard reachable")
		}
		plain := bufferPlain(paintAccountSurface(t, s, size.W, size.H))
		if !strings.Contains(plain, "manifest.json") || !strings.Contains(plain, "files/server") {
			t.Fatalf("exact managed files were not rendered at %+v:\n%s", size, plain)
		}
	}
	if result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.code-tools.confirm-remove"}); result.Cmd != nil {
		t.Fatal("unreviewed action removed tools")
	}
	applyCodeToolsCommand(t, m, s, "settings.code-tools.remove")
	if stub.removes != 0 || m.codeTools.preview == nil || stub.previews != 1 {
		t.Fatal("preview changed files or missing review")
	}
	for _, size := range []ui.Size{{W: 124, H: 30}, {W: 80, H: 24}} {
		plain := bufferPlain(paintAccountSurface(t, s, size.W, size.H))
		if !strings.Contains(plain, "Review removal") || !strings.Contains(plain, "Will remove") || !strings.Contains(plain, "Remove") || !strings.Contains(plain, "Close") {
			t.Fatalf("review not rendered at %+v:\n%s", size, plain)
		}
	}
	result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.code-tools.remove"})
	prompt := result.Cmd().(PromptMsg)
	if !s.PromptRelevant(prompt) || prompt.Kind != PromptRemoveCodeTools || stub.removes != 0 {
		t.Fatal("native confirmation missing")
	}
	applyCodeToolsCommand(t, m, s, "settings.code-tools.confirm-remove")
	if stub.removes != 1 || s.PromptRelevant(prompt) || len(m.codeTools.inventory.Tools) != 0 || m.codeTools.preview != nil {
		t.Fatal("remove outcome did not refresh/invalidate review")
	}
}

func TestCodeToolsSettingsUnusedCancellationAndStaleResults(t *testing.T) {
	m, s, stub := codeToolsFixture(t)
	applyCodeToolsCommand(t, m, s, "settings.code-tools.remove-unused")
	if !stub.unused || len(stub.ids) != 0 {
		t.Fatal("Remove unused selected a different operation")
	}
	prompt := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.code-tools.remove"}).Cmd().(PromptMsg)
	m.SelectCategory(catEditor)
	if s.PromptRelevant(prompt) || m.codeTools.preview != nil {
		t.Fatal("page switch retained consent")
	}
	m.SelectCategory(catCodeTools)
	s.SetModel(m)
	cmd := m.beginCodeTools("refresh", nil, false, 0)
	s.CancelCodeTools()
	before := stub.reads
	m.Update(cmd())
	if stub.reads != before || m.codeTools.busy {
		t.Fatal("closed Settings started queued I/O")
	}
	m.Update(CodeToolsResultMsg{Target: m, Generation: m.codeTools.generation - 1, Operation: "preview", Preview: stub.preview})
	if m.codeTools.preview != nil {
		t.Fatal("old result reopened removal")
	}
}

func TestCodeToolsSettingsMountedInventoryAndFailure(t *testing.T) {
	m, s, stub := codeToolsFixture(t)
	for _, size := range []ui.Size{{W: 124, H: 30}, {W: 80, H: 24}} {
		host := ui.NewHost(theme.DefaultDark(), s)
		host.Resize(size.W, size.H)
		host.Layout()
		if !s.form.FocusField("code-tools-unmanaged-0") {
			t.Fatal("unmanaged tool not keyboard reachable")
		}
		plain := bufferPlain(paintAccountSurface(t, s, size.W, size.H))
		if !strings.Contains(plain, "Not managed") || !strings.Contains(plain, "Crude never") || !strings.Contains(plain, "removes this tool.") {
			t.Fatalf("unmanaged protection not rendered:\n%s", plain)
		}
		if strings.Contains(plain, "[ Save ]") {
			t.Fatal("inventory offered preference Save")
		}
	}
	if m.fieldAt(catCodeTools, "settings.code-tools.remove/C:/user/bin/server") != nil {
		t.Fatal("unmanaged tool offered removal")
	}
	stub.err = errors.New("disk unavailable")
	applyCodeToolsCommand(t, m, s, "settings.code-tools.refresh")
	if !strings.Contains(m.codeTools.notice, "disk unavailable") || m.codeTools.busy {
		t.Fatal("read failure missing")
	}
}

func TestCodeToolsSettingsRemoveFailureObservesInventoryWithoutReplay(t *testing.T) {
	m, s, stub := codeToolsFixture(t)
	applyCodeToolsCommand(t, m, s, "settings.code-tools.remove-unused")
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.code-tools.remove"})
	stub.removeErr = errors.New("could not clean the removal folder")
	applyCodeToolsCommand(t, m, s, "settings.code-tools.confirm-remove")
	if stub.removes != 1 || stub.reads != 2 || len(m.codeTools.inventory.Tools) != 0 ||
		m.codeTools.preview != nil || !strings.Contains(m.codeTools.notice, "could not clean") {
		t.Fatal("removal error did not observe the actual store without replay")
	}
}
