package workspace

import (
	"encoding/json"
	"errors"
	"slices"
)

type ToolViewState struct {
	Width     int      `json:"width,omitempty"`
	Filter    string   `json:"filter,omitempty"`
	Selection string   `json:"selection,omitempty"`
	Offset    int      `json:"offset,omitempty"`
	Collapsed []string `json:"collapsed,omitempty"`
}

type ToolLayout struct {
	Rail         []string                 `json:"rail"`
	Views        map[string]ToolViewState `json:"views,omitempty"`
	PanelHeights map[string]int           `json:"panel_heights,omitempty"`
}

func DefaultToolLayout() ToolLayout {
	return ToolLayout{Rail: []string{"files", "agents", "git", "skills", "mcp"}, Views: make(map[string]ToolViewState), PanelHeights: make(map[string]int)}
}

func (workspace *Workspace) LoadToolLayout() (ToolLayout, error) {
	layout := DefaultToolLayout()
	if workspace.WorkspaceDB == nil {
		return layout, nil
	}
	raw := workspace.WorkspaceDB.GetSetting("tool_layout")
	if raw == "" {
		return layout, nil
	}
	if err := json.Unmarshal([]byte(raw), &layout); err != nil {
		return DefaultToolLayout(), err
	}
	if err := validateToolLayout(layout); err != nil {
		return DefaultToolLayout(), err
	}
	if layout.Views == nil {
		layout.Views = make(map[string]ToolViewState)
	}
	if layout.PanelHeights == nil {
		layout.PanelHeights = make(map[string]int)
	}
	return layout, nil
}

func (workspace *Workspace) SaveToolLayout(layout ToolLayout) error {
	if err := validateToolLayout(layout); err != nil {
		return err
	}
	if workspace.WorkspaceDB == nil {
		return nil
	}
	encoded, err := json.Marshal(layout)
	if err != nil {
		return err
	}
	return workspace.WorkspaceDB.SetSetting("tool_layout", string(encoded))
}

func validateToolLayout(layout ToolLayout) error {
	known := DefaultToolLayout().Rail
	seen := make(map[string]bool)
	for _, id := range layout.Rail {
		if !slices.Contains(known, id) || seen[id] {
			return errors.New("invalid workspace navigation identity or order")
		}
		seen[id] = true
	}
	for id, view := range layout.Views {
		if !slices.Contains(known, id) {
			return errors.New("invalid workspace tool view identity")
		}
		if view.Width < 0 || view.Width > 1000 || view.Offset < 0 || len(view.Filter)+len(view.Selection) > 4096 {
			return errors.New("invalid workspace view geometry or state")
		}
	}
	for panel, height := range layout.PanelHeights {
		if !slices.Contains([]string{"terminal", "workspace", "problems", "notices", "memory", "board"}, panel) || height < 0 || height > 1000 {
			return errors.New("invalid workspace panel height or identity")
		}
	}
	return nil
}
