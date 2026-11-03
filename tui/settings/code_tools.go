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

func (m *Model) buildCodeToolsForm() *ui.FormModel {
	r := &m.codeTools
	var fields []*ui.FieldState
	if r.preview != nil {
		fields = append(fields, remoteInfo("code-tools-delete-size", "Will remove", fmt.Sprintf("%s (%d bytes)", codeToolSize(r.preview.Bytes), r.preview.Bytes), "Only these managed tool folders and their contents will be removed."))
		for i, tool := range r.preview.Tools {
			fields = append(fields, codeToolText(fmt.Sprintf("code-tools-delete-%d", i), tool.Package+" "+tool.Version, toolDescription(tool)+"\n\nFiles to remove\n"+toolFiles(tool)))
		}
		return formModel(fields...)
	}
	for _, t := range r.inventory.Tools {
		if r.detail == t.ID {
			fields = append(fields, codeToolText("code-tools-overview", "Tool details", toolDescription(t)), codeToolText("code-tools-files", "Files", toolFiles(t)))
			if r.notice != "" {
				fields = append(fields, codeToolText("code-tools-detail-notice", "Status", r.notice), remoteOperation("settings.code-tools.refresh", "Refresh", "Read tools", "Read the tools again after an error.", r.busy))
			}
			return formModel(fields...)
		}
	}
	status := r.notice
	if status == "" {
		switch {
		case r.service == nil:
			status = "Tool inventory is not available from this connection."
		case r.busy:
			status = "Loading tools…"
		case !r.loaded:
			status = "Choose Refresh to read the tools."
		case len(r.inventory.Tools) == 0:
			status = "Crude has not installed any tools."
		default:
			status = "User-installed tools are listed below and cannot be removed."
		}
	}
	fields = append(fields, remoteInfo("code-tools-status", "Tools", status, "The shared inventory belongs to the selected core."), remoteInfo("code-tools-size", "Total managed size", codeToolSize(r.inventory.TotalBytes), "Content size of all tools installed by Crude."))
	fields = append(fields, remoteOperation("settings.code-tools.refresh", "Refresh", "Read tools", "Read current tools and history without changing files.", r.service == nil || r.busy))
	for _, t := range r.inventory.Tools {
		fields = append(fields, remoteOperation("settings.code-tools.open/"+t.ID, t.Package+" "+t.Version, codeToolSize(t.Size), "Review the source, files, approval and workspace use.", r.busy))
	}
	for i, t := range r.inventory.Unmanaged {
		fields = append(fields, codeToolText(fmt.Sprintf("code-tools-unmanaged-%d", i), t.Name+" (not managed by Crude)", "Not managed by Crude.\nCrude never removes this tool.\nCommand: "+t.Command+"\nExecutable: "+t.Executable+"\nSource: "+t.Source))
	}
	if len(r.inventory.Unrecognized) > 0 {
		fields = append(fields, codeToolText("code-tools-unrecognized", "Other store entries", strings.Join(r.inventory.Unrecognized, "\n")+"\nCrude never removes these entries."))
	}
	var history strings.Builder
	for _, e := range r.inventory.History {
		fmt.Fprintf(&history, "%s  %s  %s %s\nBy: %s\nApproval: %s\n%s\n\n", codeToolTime(e.Time, "Time not recorded"), codeToolHistoryKind(e.Kind), e.Package, e.Version, e.By, e.Approval, e.Detail)
	}
	fields = append(fields, codeToolText("code-tools-history", "History", mapDefault(history.String(), "No tool history was recorded.")))
	offset := r.inventory.HistoryOffset
	if offset > 0 {
		fields = append(fields, remoteOperation("settings.code-tools.newer", "Newer history", "Previous page", "Read newer history without changing tools.", r.busy))
	}
	if offset+len(r.inventory.History) < r.inventory.HistoryTotal {
		fields = append(fields, remoteOperation("settings.code-tools.older", "Older history", "Next page", "Read older history without changing tools.", r.busy))
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

func (m *Model) rebuildCodeTools() {
	if len(m.categories) > catCodeTools {
		m.categories[catCodeTools].Form = m.buildCodeToolsForm()
	}
}

func (s *WorkspaceSettingsSurface) SetCodeTools(service shellport.CodeTools) tea.Cmd {
	if s.model == nil {
		return nil
	}
	m := s.model
	m.cancelCodeTools()
	m.codeTools.service = service
	m.rebuildCodeTools()
	cmd := m.beginCodeTools("refresh", nil, false, 0)
	s.refreshForm()
	return cmd
}

func (s *WorkspaceSettingsSurface) CancelCodeTools() {
	if s.model != nil {
		s.model.cancelCodeTools()
		s.model.rebuildCodeTools()
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
	m.rebuildCodeTools()
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
	m.rebuildCodeTools()
	return nil
}

func (s *WorkspaceSettingsSurface) codeToolsPromptRelevant(msg PromptMsg) bool {
	m := s.model
	return m != nil && msg.Target == m && m.selected == catCodeTools && !m.codeTools.busy &&
		msg.codeToolGeneration == m.codeTools.generation && msg.CodeToolPreview != nil &&
		reflect.DeepEqual(msg.CodeToolPreview, m.codeTools.preview)
}

func (s *WorkspaceSettingsSurface) handleCodeToolsAction(action ui.Action) ui.EventResult {
	result := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
	m := s.model
	if m == nil || m.selected != catCodeTools || m.codeTools.busy {
		return result
	}
	r := &m.codeTools
	switch {
	case action.ID == "settings.code-tools.refresh":
		result.Cmd = m.beginCodeTools("refresh", nil, false, 0)
	case action.ID == "settings.code-tools.older":
		result.Cmd = m.beginCodeTools("refresh", nil, false, r.inventory.HistoryOffset+50)
	case action.ID == "settings.code-tools.newer":
		result.Cmd = m.beginCodeTools("refresh", nil, false, max(0, r.inventory.HistoryOffset-50))
	case strings.HasPrefix(string(action.ID), "settings.code-tools.open/"):
		r.detail = strings.TrimPrefix(string(action.ID), "settings.code-tools.open/")
	case action.ID == "settings.code-tools.back":
		m.cancelCodeTools()
		r.detail, r.notice = "", ""
	case action.ID == "settings.code-tools.remove-unused":
		result.Cmd = m.beginCodeTools("preview", nil, true, 0)
	case action.ID == "settings.code-tools.remove":
		if r.preview != nil {
			p := PromptMsg{Target: m, Kind: PromptRemoveCodeTools, CodeToolPreview: r.preview, codeToolGeneration: r.generation}
			r.prompt = &p
			result.Cmd = func() tea.Msg { return p }
		} else if r.detail != "" {
			result.Cmd = m.beginCodeTools("preview", []string{r.detail}, false, 0)
		}
	case action.ID == "settings.code-tools.confirm-remove":
		if r.prompt != nil && s.codeToolsPromptRelevant(*r.prompt) {
			result.Cmd = m.beginCodeTools("remove", nil, false, 0)
		}
	}
	m.rebuildCodeTools()
	s.syncCodeToolsBar()
	s.refreshForm()
	return result
}

func (s *WorkspaceSettingsSurface) syncCodeToolsBar() {
	if s.model == nil || s.codeToolsBar == nil {
		return
	}
	r := &s.model.codeTools
	buttons := []ui.ActionBarButton{}
	if r.detail != "" || r.preview != nil {
		buttons = append(buttons, ui.ActionBarButton{Label: "Remove", Action: "settings.code-tools.remove", Variant: ui.VariantDanger, Enabled: !r.busy && r.service != nil},
			ui.ActionBarButton{Label: "Back", Action: "settings.code-tools.back", Enabled: !r.busy})
	} else {
		buttons = append(buttons, ui.ActionBarButton{Label: "Remove unused", Action: "settings.code-tools.remove-unused", Variant: ui.VariantDanger, Enabled: !r.busy && r.loaded && len(r.inventory.Tools) > 0})
	}
	if !reflect.DeepEqual(s.codeToolsButtons, buttons) {
		s.codeToolsBar.SetButtons(buttons...)
		s.codeToolsButtons = buttons
	}
}
