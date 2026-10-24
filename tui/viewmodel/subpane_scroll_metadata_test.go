package viewmodel

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/types"
	"crude/workspace"
)

type recursiveScrollFixture struct {
	registry *subPaneProjectionRegistry
	panes    *workspace.PaneManager
	main     *chatprojection.Renderer
}

func newRecursiveScrollFixture(t *testing.T, width int, hydrate bool) recursiveScrollFixture {
	t.Helper()
	reg := newSubPaneProjectionRegistry(theme.DefaultDark())
	panes := workspace.NewPaneManager()
	for _, id := range []string{"outer", "child", "leaf"} {
		pane := panes.GetOrCreate(id, workspace.PaneThinking, id)
		pane.StartTime = time.Unix(1, 0)
		if id == "leaf" {
			var body []string
			for row := range 12 {
				body = append(body, fmt.Sprintf("LEAF-%02d", row))
			}
			pane.Messages.AppendMessage(workspace.Message{InternalID: "leaf-body", Role: types.RoleThinking, Content: strings.Join(body, "\n")})
		} else {
			nested := "child"
			if id == "child" {
				nested = "leaf"
			}
			pane.Messages.AppendMessage(workspace.Message{InternalID: id + "-body", Role: types.RoleTool, SubPaneID: nested})
		}
		projection := reg.Get(id, pane.Type)
		projection.scroll.SetAutoScroll(false)
		messages := ChatMessagesFromViews(pane.Messages.Messages())
		if hydrate {
			messages = hydrateScrollMessages(t, pane.Messages.Messages())
		}
		projection.renderer.SyncMessages(messages, 1, 0)
	}
	render := func(id string, width, maxH, frame int, expanded bool) []chatprojection.SubPaneLine {
		return reg.Render(id, panes.Get(id), width, maxH, frame, expanded)
	}
	for _, projection := range reg.projections {
		projection.renderer.SetSubPaneContentFn(render)
	}
	main := chatprojection.NewRenderer(reg.theme)
	main.SetSize(width, 60)
	main.SetChatAreaBounds(0, 60)
	main.SetSubPaneContentFn(render)
	root := []workspace.Message{{InternalID: "main-body", Role: types.RoleTool, SubPaneID: "outer"}}
	messages := ChatMessagesFromViews(root)
	if hydrate {
		messages = hydrateScrollMessages(t, root)
	}
	main.SyncMessages(messages, 1, 0)
	return recursiveScrollFixture{registry: reg, panes: panes, main: main}
}

func hydrateScrollMessages(t *testing.T, snapshots []workspace.Message) []types.ChatMessage {
	t.Helper()
	database := sessiondb.OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { database.Close() })
	if err := sessiondb.CreateSessionRow(database, "scroll-session"); err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range snapshots {
		wire := provider.NewTextMessage("assistant", snapshot.Content)
		if err := sessiondb.AppendMessageWithMeta(database, "scroll-session", wire, 1, sessiondb.DisplayMeta{
			SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []types.ConversationMessage{snapshot},
		}); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := sessiondb.LoadMessagesWithMeta(database)
	if err != nil {
		t.Fatal(err)
	}
	return ChatMessagesFromViews(workspace.ConvertMessagesForDisplay(stored))
}

func TestRecursiveSubpaneScrollMetadataLiveAndHydrated(t *testing.T) {
	for _, width := range []int{17, 53, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			live := newRecursiveScrollFixture(t, width, false)
			hydrated := newRecursiveScrollFixture(t, width, true)
			want := []chatprojection.PaneLineInfo{
				{},
				{PaneID: "outer", LineIdx: -1},
				{PaneID: "outer", LineIdx: 0}, // child's leading spacer
				{PaneID: "child", LineIdx: -1, Ancestors: []chatprojection.PaneTarget{{PaneID: "outer", LineIdx: 1}}},
				{PaneID: "child", LineIdx: 0, Ancestors: []chatprojection.PaneTarget{{PaneID: "outer", LineIdx: 2}}}, // leaf's leading spacer
				{PaneID: "leaf", LineIdx: -1, Ancestors: []chatprojection.PaneTarget{{PaneID: "outer", LineIdx: 3}, {PaneID: "child", LineIdx: 1}}},
				{PaneID: "leaf", LineIdx: 0, Ancestors: []chatprojection.PaneTarget{{PaneID: "outer", LineIdx: 4}, {PaneID: "child", LineIdx: 2}}},
				{PaneID: "leaf", LineIdx: 1, Ancestors: []chatprojection.PaneTarget{{PaneID: "outer", LineIdx: 5}, {PaneID: "child", LineIdx: 3}}},
				{PaneID: "leaf", LineIdx: 2, Ancestors: []chatprojection.PaneTarget{{PaneID: "outer", LineIdx: 6}, {PaneID: "child", LineIdx: 4}}},
				{PaneID: "leaf", LineIdx: 3, Ancestors: []chatprojection.PaneTarget{{PaneID: "outer", LineIdx: 7}, {PaneID: "child", LineIdx: 5}}},
			}
			left := live.main.ExportMessageRowsSnapshot(width)
			right := hydrated.main.ExportMessageRowsSnapshot(width)
			for path, snapshot := range map[string]chatprojection.MessageRowsSnapshot{"live": left, "hydrated": right} {
				if !reflect.DeepEqual(snapshot.PaneLineMap, want) {
					t.Fatalf("%s recursive map:\ngot  %+v\nwant %+v", path, snapshot.PaneLineMap, want)
				}
				if len(snapshot.Rows) != 1 || len(snapshot.Rows[0].Lines) != 10 || !strings.Contains(snapshot.Rows[0].Lines[7], "LEAF-00") || !strings.Contains(snapshot.Rows[0].Lines[8], "LEAF-01") {
					t.Fatalf("%s lost actual cropped leaf content: %+v", path, snapshot.Rows)
				}
			}
			gutters := live.registry.BuildPaneGutters([]chatprojection.PaneLineInfo{want[7]}, live.panes)
			if len(gutters) != 3 {
				t.Fatalf("leaf-only metadata omitted ancestor models: %+v", gutters)
			}
			for _, id := range []string{"outer", "child", "leaf"} {
				if gutters[id].Scroll != live.registry.ScrollModel(id) {
					t.Fatalf("%s gutter borrowed wrong registry/lifetime", id)
				}
			}
			if !gutters["child"].Visible || !gutters["leaf"].Visible || gutters["outer"].Visible {
				t.Fatalf("actual overflow visibility = %+v", gutters)
			}
			// The outer thinking renderer has shorter content than its viewport.
			// RenderVisible gravity and global export must still select the same rows.
			visible, _ := live.main.RenderVisible(width)
			viewportMap := live.main.PaneLineMap()
			pad := len(visible) - len(want)
			if pad <= 0 || !reflect.DeepEqual(viewportMap[pad:], want) {
				t.Fatalf("gravity/global disagreement: pad%d map%+v", pad, viewportMap)
			}
			for _, info := range viewportMap[:pad] {
				if info.PaneID != "" || len(info.Ancestors) != 0 {
					t.Fatal("gravity padding acquired pane ownership")
				}
			}
			// A leaf scroll must refresh both containing renderers without moving
			// their offsets. Otherwise the outer retained row keeps stale pixels.
			live.registry.ScrollModel("leaf").ScrollTo(3)
			live.registry.InvalidatePaneChain([]string{"outer", "child", "leaf"})
			live.main.InvalidateSubPane("outer")
			leafScrolled := live.main.ExportMessageRowsSnapshot(width)
			if !strings.Contains(leafScrolled.Rows[0].Lines[6], "LEAF-02") || !reflect.DeepEqual(leafScrolled.PaneLineMap, want) {
				t.Fatalf("descendant scroll did not refresh exact retained chain: map%+v rows%+v", leafScrolled.PaneLineMap, leafScrolled.Rows)
			}
			live.registry.ScrollModel("leaf").ScrollTo(0)
			live.registry.InvalidatePaneChain([]string{"outer", "child", "leaf"})
			live.main.InvalidateSubPane("outer")
			// Scroll the child so the leaf header moves to body row zero. The
			// grandchild's viewport index stays exact instead of a guessed offset.
			live.registry.ScrollModel("child").ScrollTo(1)
			live.registry.InvalidatePaneChain([]string{"outer", "child"})
			live.main.InvalidateSubPane("outer")
			scrolled := live.main.ExportMessageRowsSnapshot(width)
			wantHeader := chatprojection.PaneLineInfo{PaneID: "leaf", LineIdx: -1, Ancestors: []chatprojection.PaneTarget{{PaneID: "outer", LineIdx: 2}, {PaneID: "child", LineIdx: 0}}}
			if !reflect.DeepEqual(scrolled.PaneLineMap[4], wantHeader) || !strings.Contains(scrolled.Rows[0].Lines[6], "LEAF-00") {
				t.Fatalf("cropped child header/body drifted: map%+v rows%+v", scrolled.PaneLineMap, scrolled.Rows)
			}
			// Collapse an ancestor: leaf scroll identities must disappear, even
			// though its retained projection/model still exists in the registry.
			live.panes.Get("child").Collapse()
			live.registry.InvalidatePaneChain([]string{"outer", "child"})
			live.main.InvalidateSubPane("outer")
			collapsed := live.main.ExportMessageRowsSnapshot(width)
			for _, info := range collapsed.PaneLineMap {
				if info.PaneID == "leaf" {
					t.Fatal("collapsed source retained invisible descendant ownership")
				}
			}
			collapsedGutters := live.registry.BuildPaneGutters(collapsed.PaneLineMap, live.panes)
			if _, ok := collapsedGutters["leaf"]; ok || collapsedGutters["child"].Visible {
				t.Fatalf("collapsed ancestor retained child gutter: %+v", collapsedGutters)
			}
			// Replace the enclosing canonical source entirely. Retained descendant
			// projections may survive, but no longer own any painted outer rows.
			replacement := workspace.NewConversation()
			replacement.AppendMessage(workspace.Message{InternalID: "replacement", Role: types.RoleThinking, Content: "REPLACED-BODY"})
			live.registry.SyncFromWorkspace("outer", replacement, false)
			live.main.InvalidateSubPane("outer")
			replaced := live.main.ExportMessageRowsSnapshot(width)
			if !strings.Contains(strings.Join(replaced.Rows[0].Lines, "\n"), "REPLACED-BODY") {
				t.Fatal("replacement content did not reach real outer render")
			}
			for _, info := range replaced.PaneLineMap {
				if info.PaneID == "child" || info.PaneID == "leaf" || len(info.Ancestors) != 0 {
					t.Fatalf("replacement source retained descendant metadata: %+v", info)
				}
			}
		})
	}
}

func TestSubpaneScrollMetadataSourceLifetimeAndCompactRows(t *testing.T) {
	reg := newSubPaneProjectionRegistry(theme.DefaultDark()).ForOwner("left")
	panes := workspace.NewPaneManager()
	pane := panes.GetOrCreate("compact", workspace.PaneAgent, "compact")
	for row := range 8 {
		pane.Messages.AppendMessage(workspace.Message{InternalID: fmt.Sprintf("tool-%d", row), Role: types.RoleTool,
			ToolName: "read_file", ToolArgs: fmt.Sprintf("file-%d.go", row), ToolStatus: types.ToolStatusSuccess, SubPaneID: "not-actually-rendered"})
	}
	lines := reg.Render(pane.ID, pane, 40, 3, 0, true)
	if lines[0].PaneInfo.PaneID != pane.ID || lines[0].PaneInfo.LineIdx != -1 {
		t.Fatalf("compact header identity = %+v", lines[0].PaneInfo)
	}
	for row, line := range lines[1:] {
		if line.PaneInfo.PaneID != pane.ID || line.PaneInfo.LineIdx != row || len(line.PaneInfo.Ancestors) != 0 {
			t.Fatalf("compact tool invented recursion: %+v", line.PaneInfo)
		}
	}
	old := reg.ScrollModel(pane.ID)
	old.SetAutoScroll(false)
	old.ScrollTo(1)
	pane.Messages.AppendMessage(workspace.Message{InternalID: "appended", Role: types.RoleTool, ToolName: "read_file", ToolArgs: "new.go", ToolStatus: types.ToolStatusSuccess})
	reg.Render(pane.ID, pane, 40, 3, 0, true)
	if reg.ScrollModel(pane.ID) != old || old.AutoScroll() || old.Offset() != 1 {
		t.Fatal("ordinary source append replaced scroll lifetime or user position")
	}
	pane.Messages = workspace.NewConversation()
	for row := range 9 { // same version/count as the old source, different identity
		pane.Messages.AppendMessage(workspace.Message{InternalID: fmt.Sprintf("replacement-%d", row), Role: types.RoleTool,
			ToolName: "read_file", ToolArgs: fmt.Sprintf("replacement-%d.go", row), ToolStatus: types.ToolStatusSuccess})
	}
	replacedLines := reg.Render(pane.ID, pane, 40, 3, 0, true)
	current := reg.ScrollModel(pane.ID)
	if current == old || current.Offset() != current.MaxOffset() || !current.AutoScroll() || !subPanePlainContains(replacedLines, "replacement-8.go") {
		t.Fatal("replacement conversation inherited old gesture model/anchor")
	}
	other := newSubPaneProjectionRegistry(theme.DefaultDark()).ForOwner("right")
	other.Render(pane.ID, pane, 60, 4, 0, true)
	gutter := reg.BuildPaneGutters([]chatprojection.PaneLineInfo{{PaneID: pane.ID, LineIdx: 0}}, panes)[pane.ID]
	if gutter.Scroll != current || gutter.Scroll == other.ScrollModel(pane.ID) {
		t.Fatal("gutter resolved another embedding owner's model")
	}
}
