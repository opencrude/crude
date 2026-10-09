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
