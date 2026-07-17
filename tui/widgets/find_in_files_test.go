package widgets

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/tui/rendering"
	"crude/tui/theme"
)

func TestFindInFilesSearchFlowAndPreview(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "handler.go")
	if err := os.WriteFile(path, []byte("package sample\n\nfunc HandleWorkspaceEvent() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewFindInFiles(theme.DefaultDark(), root)

	debounceCmd := m.HandleEvent(tea.KeyPressMsg{Code: 'h', Text: "hwe"})
	if debounceCmd == nil || m.Query() != "hwe" {
		t.Fatalf("typing query=%q cmd=%v", m.Query(), debounceCmd)
	}
	requestMsg, ok := debounceCmd().(FindInFilesSearchRequestMsg)
	if !ok {
		t.Fatalf("debounce returned %T", debounceCmd())
	}
	if requestMsg.Query != "hwe" || requestMsg.Options.Regex || requestMsg.Options.CaseSensitive {
		t.Fatalf("search intent=%+v", requestMsg)
	}
	results, err := fileio.SearchProject(context.Background(), root, requestMsg.Query, requestMsg.Options)
	if err != nil {
		t.Fatal(err)
	}
	m.ApplySearchResults(requestMsg.Generation, results, nil, 0)
	if len(m.results) == 0 || m.selected != 0 || m.preview == nil {
		t.Fatalf("results=%d selected=%d preview=%v", len(m.results), m.selected, m.preview)
	}

	view := m.View(100, 24, 0)
	plain := stripAnsi(view)
	if !strings.Contains(plain, "handler.go:3") || !strings.Contains(plain, "HandleWorkspaceEvent") {
		t.Fatalf("search view missing result/preview:\n%s", plain)
	}
	for i, line := range strings.Split(view, "\n") {
		if got := rendering.VisibleWidth(line); got != 100 {
			t.Fatalf("line %d width=%d", i, got)
		}
	}
}

func TestFindInFilesSingleClickPreviewsDoubleClickOpens(t *testing.T) {
	m := NewFindInFiles(theme.DefaultDark(), t.TempDir())
	m.ApplySearchResults(0, []fileio.ProjectSearchMatch{
		{Path: "/tmp/main.go", RelativePath: "main.go", Line: 7, Column: 2, EndColumn: 6, LineText: "  value", ContextStart: 6, Context: "before\n  value\nafter"},
	}, nil, 0)
	m.View(90, 22, 0)
	click := tea.MouseClickMsg{X: 5, Y: m.resultY0, Button: tea.MouseLeft}
	if cmd := m.HandleEvent(click); cmd != nil {
		t.Fatal("single click should select/preview without opening")
	}
	if m.preview == nil {
		t.Fatal("single click should retain preview")
	}
	cmd := m.HandleEvent(click)
	if cmd == nil {
		t.Fatal("double click should open selected result")
	}
	if _, ok := cmd().(FindInFilesOpenMsg); !ok {
		t.Fatalf("double click returned unexpected message")
	}
}

func TestFindInFilesTogglesAndFilenameOnlyPreview(t *testing.T) {
	m := NewFindInFiles(theme.DefaultDark(), t.TempDir())
	m.View(90, 22, 0)
	m.HandleEvent(tea.MouseClickMsg{X: m.caseX0, Y: 0, Button: tea.MouseLeft})
	if !m.caseSensitive {
		t.Fatal("Cc control should toggle case-sensitive search")
	}
	m.HandleEvent(tea.MouseClickMsg{X: m.regexX0, Y: 0, Button: tea.MouseLeft})
	if !m.regex {
		t.Fatal(".* control should toggle regex search")
	}

	m.ApplySearchResults(m.generation, []fileio.ProjectSearchMatch{
		{Path: "/tmp/workspace_registry.md", RelativePath: "workspace_registry.md", NameOnly: true},
	}, nil, 0)
	if m.preview != nil {
		t.Fatal("filename-only result must leave preview empty")
	}
}

func TestFindInFilesIgnoresStaleGenerationResults(t *testing.T) {
	m := NewFindInFiles(theme.DefaultDark(), t.TempDir())
	m.generation = 2
	m.ApplySearchResults(1, []fileio.ProjectSearchMatch{
		{Path: "/tmp/stale.go", RelativePath: "stale.go"},
	}, nil, 0)
	if len(m.results) != 0 {
		t.Fatal("results from a previously closed search window must be ignored")
	}
}

func TestFindInFilesScrollbarDragAndEnter(t *testing.T) {
	m := NewFindInFiles(theme.DefaultDark(), t.TempDir())
	results := make([]fileio.ProjectSearchMatch, 40)
	for i := range results {
		results[i] = fileio.ProjectSearchMatch{Path: "/tmp/main.go", RelativePath: "main.go", Line: i + 1, LineText: "match", ContextStart: i + 1, Context: "match"}
	}
	m.ApplySearchResults(0, results, nil, 0)
	view := m.View(90, 22, 0)
	for i, line := range strings.Split(view, "\n") {
		if got := rendering.VisibleWidth(line); got != 90 {
			t.Fatalf("scrolling view line %d width=%d", i, got)
		}
	}
	m.HandleEvent(tea.MouseClickMsg{X: 88, Y: m.resultY0 + m.resultH - 1, Button: tea.MouseLeft})
	if m.scroll == 0 || !m.scrollDragging {
		t.Fatalf("scrollbar click should jump and start drag: scroll=%d dragging=%v", m.scroll, m.scrollDragging)
	}
	m.HandleEvent(tea.MouseReleaseMsg{X: 89, Y: m.resultY0, Button: tea.MouseLeft})
	if m.scrollDragging {
		t.Fatal("mouse release should stop scrollbar drag")
	}
	cmd := m.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should open selected result")
	}
	if _, ok := cmd().(FindInFilesOpenMsg); !ok {
		t.Fatal("Enter returned wrong message")
	}
}
