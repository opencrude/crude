package workspace

// TreeRowView is the presentation-neutral data for one visible file-tree row.
// It deliberately contains no filetree model, renderer, hover object, or
// theme value. The TUI decides how these facts are painted and interacted with.
type TreeRowView struct {
	Path               string
	Name               string
	Depth              int
	IsDir              bool
	IsSymlink          bool
	Expanded           bool
	Loading            bool
	Hidden             bool
	GitState           int
	AggregateGitState  int
	DiagnosticCount    int
	DiagnosticErrors   int
	DiagnosticWarnings int
	DiagnosticHints    int
	Selected           bool
	Cursor             bool
	Hovered            bool
}

// TreeView is a stable snapshot of the file-tree projection. Rows are ordered
// exactly as displayed; selection and scroll intent are data, not renderer
// state.
type TreeView struct {
	Rows          []TreeRowView
	SelectedPath  string
	CursorIndex   int
	ScrollOffset  int
	Filter        string
	FilterActive  bool
	Width         int
	VisibleHeight int
}

// TabView is the neutral tab data consumed by the TUI tab control.
type TabView struct {
	ID           string
	Title        string
	DisplayTitle string
	Icon         string
	Kind         string
	State        string
	Closable     bool
	Pinned       bool
	Active       bool
}

// AgentTabViews returns the current agent tabs in forest order. Window IDs,
// layout, hover, and close interaction remain TUI concerns.
func (w *Workspace) AgentTabViews() []TabView {
	if w == nil || w.AgentMgr == nil {
		return nil
	}
	nodes := w.AgentMgr.AllNodes()
	views := make([]TabView, 0, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		state := ""
		if node.State != nil {
			state = node.State.Status
		}
		kind := "orchestrator"
		if node.Parent != nil {
			kind = "sub-agent"
		}
		label := AgentDisplayLabel(node)
		views = append(views, TabView{
			ID:           node.ID,
			Title:        "agent:" + node.ID,
			DisplayTitle: label,
			Kind:         kind,
			State:        state,
			Closable:     true,
			Active:       node.ID == w.AgentMgr.ActiveID(),
		})
	}
	return views
}

// StatusView is the workspace-owned status snapshot consumed by the status
// control. It contains no rendering strings, styles, or widget state.
type StatusView struct {
	CurrentAgentID string
	ModelName      string
	Username       string
	Usage          UsageSnapshot
	AgentCount     int
	ActiveAgents   int
	Blocking       bool
}

func (w *Workspace) StatusView() StatusView {
	view := StatusView{}
	if w == nil {
		return view
	}
	if w.AgentMgr == nil {
		view.ModelName = w.ModelName
		view.Username = w.Username
		return view
	}
	view.CurrentAgentID = w.AgentMgr.ActiveID()
	view.ModelName = w.ModelName
	view.Username = w.Username
	view.AgentCount = w.AgentMgr.Count()
	for _, node := range w.AgentMgr.AllNodes() {
		if node == nil || node.State == nil {
			continue
		}
		if node.State.IsActive() {
			view.ActiveAgents++
		}
		if node.State.PendingPermTool != "" {
			view.Blocking = true
		}
	}
	if usage, ok := w.AgentUsage(view.CurrentAgentID); ok {
		view.Usage = usage
	}
	return view
}
