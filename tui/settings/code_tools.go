package settings

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/ui"
	"crude/types"
)

type codeToolsSettingsState struct {
	service        shellport.CodeTools
	generation     uint64
	cancel         context.CancelFunc
	busy, loaded   bool
	notice, detail string
	inventory      types.CodeToolInventory
	preview        *types.CodeToolRemovalPreview
	prompt         *PromptMsg
}

type CodeToolsResultMsg struct {
	Target            *Model
	Generation        uint64
	Operation         string
	Inventory         types.CodeToolInventory
	Preview           types.CodeToolRemovalPreview
	Err, RefreshError error
}

// OpenProjectToolsMsg asks the shell to open its native project setup review.
// It grants no installation authority and retains the Settings window below it.
type OpenProjectToolsMsg struct {
	Target     *Model
	Generation uint64
	ToolID     string
	Selection  types.ProjectToolSetupRequest
}

func (s *WorkspaceSettingsSurface) ProjectToolsRequestRelevant(msg OpenProjectToolsMsg) bool {
	if s.model == nil || s.model != msg.Target || s.model.selected != catLanguageServers ||
		s.model.codeTools.generation != msg.Generation || s.lsp.draft != nil ||
		s.lsp.history || s.model.codeTools.preview != nil {
		return false
	}
	if msg.ToolID == "" {
		return s.lsp.detail == nil && msg.Selection == (types.ProjectToolSetupRequest{Explicit: true})
	}
	if s.lsp.detail == nil || s.lsp.detail.id != msg.ToolID ||
		s.lsp.detail.managed != nil || s.lsp.detail.external != nil {
		return false
	}
	selection, ok := s.lsp.detail.setupTarget(s.model.codeTools.inventory.Project)
	return ok && selection == msg.Selection
}

func codeToolSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d bytes", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}

func codeToolTime(t time.Time, empty string) string {
	if t.IsZero() {
		return empty
	}
	return t.Local().Format(time.DateTime)
}

func codeToolText(id, label, value string) *ui.FieldState {
	f := ui.TextAreaFieldState(id, label, value, "Read and copy the complete details.")
	f.ReadOnly, f.Rows = true, 8
	return f
}

func toolDescription(t types.CodeTool) string {
	checksumSource := "Not recorded"
	if t.DigestOrigin == "expected" {
		checksumSource = "Checked against the source checksum"
	}
	if t.DigestOrigin == "first-download" {
		checksumSource = "Recorded at first download; the source provided no checksum"
	}
	return fmt.Sprintf("Name: %s\nVersion: %s\nSize: %s (%d bytes)\nFolder: %s\nExecutable: %s\nSource: %s\nDownload: %s\nSHA-256: %s\nChecksum source: %s\nLicense: %s\nInstalled: %s\nInstalled by: %s\nApproval: %s\nWorkspaces: %s\nLast used: %s",
		t.Package, t.Version, codeToolSize(t.Size), t.Size, t.Dir, t.Executable, t.Source, t.URL, t.SHA256, checksumSource,
		mapDefault(strings.Join(t.Licenses, ", "), "Not recorded"), codeToolTime(t.InstalledAt, "Not recorded"), t.InstalledBy, t.Approval,
		mapDefault(strings.Join(t.Workspaces, "\n"), "No recorded workspace use"), codeToolTime(t.LastUsed, "Not used yet"))
}

func toolFiles(t types.CodeTool) string {
	var b strings.Builder
	for _, f := range t.FileEntries {
		fmt.Fprintf(&b, "%s  %s  %d bytes", f.Path, f.Kind, f.Size)
		if f.LinkTarget != "" {
			fmt.Fprintf(&b, " → %s", f.LinkTarget)
		}
		b.WriteByte('\n')
	}
	return mapDefault(b.String(), "No files were reported.")
}

func buildCodeToolRemovalForm(preview *types.CodeToolRemovalPreview) *ui.FormModel {
	var fields []*ui.FieldState
	if preview != nil {
		fields = append(fields, remoteInfo("code-tools-delete-size", "Will remove", fmt.Sprintf("%s (%d bytes)", codeToolSize(preview.Bytes), preview.Bytes), "Only these managed tool folders and their contents will be removed."))
		for i, tool := range preview.Tools {
			fields = append(fields, codeToolText(fmt.Sprintf("code-tools-delete-%d", i), tool.Package+" "+tool.Version, toolDescription(tool)+"\n\nFiles to remove\n"+toolFiles(tool)))
		}
	}
	return formModel(fields...)
}

func codeToolHistoryKind(kind string) string {
	switch kind {
	case "install":
		return "Installed"
	case "install-failed":
		return "Installation failed"
	case "remove":
		return "Removed"
	case "use":
		return "First used"
	default:
		return "Tool event"
	}
}

func (s *WorkspaceSettingsSurface) SetCodeTools(service shellport.CodeTools) tea.Cmd {
	if s.model == nil {
		return nil
	}
	m := s.model
	m.cancelCodeTools()
	m.codeTools.service = service
	cmd := m.beginCodeTools("refresh", nil, false, 0)
	s.refreshForm()
	return cmd
}

func (s *WorkspaceSettingsSurface) CancelCodeTools() {
	if s.model != nil {
		s.model.cancelCodeTools()
	}
}

func (m *Model) cancelCodeTools() {
	r := &m.codeTools
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.generation++
	r.busy, r.preview, r.prompt = false, nil, nil
}

func (m *Model) beginCodeTools(operation string, ids []string, unused bool, offset int) tea.Cmd {
	r := &m.codeTools
	if r.busy || r.service == nil {
		return nil
	}
	preview := r.preview
	m.cancelCodeTools()
	r.busy, r.notice = true, "Loading tools…"
	if operation == "preview" {
		r.notice = "Preparing removal review…"
	}
	if operation == "remove" {
		r.notice = "Removing reviewed tools…"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	r.cancel = cancel
	service, generation := r.service, r.generation
	return func() tea.Msg {
		defer cancel()
		out := CodeToolsResultMsg{Target: m, Generation: generation, Operation: operation}
		if out.Err = ctx.Err(); out.Err != nil {
			return out
		}
		switch operation {
		case "preview":
			out.Preview, out.Err = service.PreviewCodeToolRemoval(ctx, ids, unused)
		case "remove":
			if preview == nil {
				out.Err = fmt.Errorf("review the removal again")
			} else {
				out.Err = service.RemoveCodeTools(ctx, *preview)
			}
		}
		// Removal can return an error after the store committed its history and
		// moved the tools. Observe the current inventory without replaying it.
		if operation != "preview" && (out.Err == nil || operation == "remove") {
			out.Inventory, out.RefreshError = service.ReadCodeToolInventory(ctx, offset, 50)
			if operation == "refresh" {
				out.Err = out.RefreshError
			}
		}
		return out
	}
}

func (m *Model) applyCodeToolsResult(msg CodeToolsResultMsg) tea.Cmd {
	r := &m.codeTools
	if msg.Target != m || msg.Generation != r.generation || !r.busy {
		return nil
	}
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.busy, r.notice = false, ""
	if msg.Err != nil {
		r.notice = "Could not " + map[string]string{"preview": "review removal", "remove": "remove tools", "refresh": "read tools"}[msg.Operation] + ". " + msg.Err.Error() + " Choose Refresh to read the current tools."
		if msg.Operation == "remove" {
			r.detail = ""
			if msg.RefreshError == nil {
				r.loaded, r.inventory = true, msg.Inventory
			}
		}
	} else if msg.Operation == "preview" {
		if len(msg.Preview.Tools) == 0 {
			r.notice = "No unused tools can be removed."
		} else {
			r.preview = &msg.Preview
		}
	} else if msg.RefreshError != nil {
		r.notice = "Removal finished. Could not refresh tools. Choose Refresh."
	} else {
		r.loaded, r.inventory = true, msg.Inventory
		if msg.Operation == "remove" {
			r.detail, r.notice = "", "Reviewed tools removed."
		}
	}
	return nil
}

func (s *WorkspaceSettingsSurface) codeToolsPromptRelevant(msg PromptMsg) bool {
	m := s.model
	return m != nil && msg.Target == m && m.selected == catLanguageServers && !m.codeTools.busy &&
		msg.codeToolGeneration == m.codeTools.generation && msg.CodeToolPreview != nil &&
		reflect.DeepEqual(msg.CodeToolPreview, m.codeTools.preview)
}

func (s *WorkspaceSettingsSurface) handleCodeToolsAction(action ui.Action) ui.EventResult {
	result := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
	m := s.model
	if m == nil || m.selected != catLanguageServers || m.codeTools.busy {
		return result
	}
	r := &m.codeTools
	switch action.ID {
	case "settings.code-tools.refresh":
		result.Cmd = m.beginCodeTools("refresh", nil, false, 0)
	case "settings.code-tools.older":
		result.Cmd = m.beginCodeTools("refresh", nil, false, r.inventory.HistoryOffset+50)
	case "settings.code-tools.newer":
		result.Cmd = m.beginCodeTools("refresh", nil, false, max(0, r.inventory.HistoryOffset-50))
	case "settings.code-tools.remove-unused":
		result.Cmd = m.beginCodeTools("preview", nil, true, 0)
	case "settings.code-tools.remove":
		if r.preview != nil {
			p := PromptMsg{Target: m, Kind: PromptRemoveCodeTools, CodeToolPreview: r.preview, codeToolGeneration: r.generation}
			r.prompt = &p
			result.Cmd = func() tea.Msg { return p }
		} else if r.detail != "" {
			result.Cmd = m.beginCodeTools("preview", []string{r.detail}, false, 0)
		}
	case "settings.code-tools.confirm-remove":
		if r.prompt != nil && s.codeToolsPromptRelevant(*r.prompt) {
			result.Cmd = m.beginCodeTools("remove", nil, false, 0)
		}
	}
	s.lsp.syncModel()
	s.refreshForm()
	return result
}
