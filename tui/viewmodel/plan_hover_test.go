package viewmodel

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"crude/tui/screens/shellchat"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types/activity"
)

func hoverPlanFixture(goal string, count int) *activity.Plan {
	p := &activity.Plan{ID: goal, Revision: 1, Status: "active", Objective: goal, Total: count, Completed: 1}
	for i := range count {
		step := activity.Step{Number: i + 1, Text: fmt.Sprintf("Verify outcome %02d", i+1), Status: "pending"}
		if i == 0 {
			step.Status = "completed"
		}
		if i == 1 {
			step.Status = "in_progress"
			p.Current = []activity.Step{step}
		}
		p.Steps = append(p.Steps, step)
	}
	return p
}

// Source injection replaces only the backend's immutable view, as in existing
// activity fixtures; mounted layout, pointer routing, window and paint are real.
func paintHoverFixture(app *App, plans map[string]*activity.Plan) {
	app.configureShell()
	for id, p := range plans {
		if view := app.mainPanes.agentViews[id]; view != nil {
			activity := app.ws.AgentActivity(id)
			activity.Plan = p
			view.chat.SetActivity(activity)
		}
	}
	app.renderShellToSurface()
	app.syncPlanHover()
}

func hoverPopoverText(app *App) string {
	if app.planHover == nil {
		return ""
	}
	fw := app.overlayHost.Window(ui.OverlayPlanHover)
	w, h := fw.Size()
	surf := surface.NewSurface("test-plan", w, h, cell.TransparentBlank())
	fw.Content.Render(surf, app.renderCtx)
	var b strings.Builder
	for y := range h {
		b.WriteString(shellchat.PlainRow(surf.Buffer(), y, w))
		b.WriteByte('\n')
	}
	return b.String()
}

func TestPlanHoverOwnsExactPaneAndDoesNotTakeDraftFocus(t *testing.T) {
	app := newFullTestApp(t)
	app.Tree.Visible = false
	app.width, app.height = 160, 40
	app.AddOrchestrator("left")
	left := app.Win.ActiveGroup()
	app.AddOrchestrator("right")
	tab := app.Win.ActiveTab().ID
	right := app.Win.SplitGroup(left, ui.Horizontal, false)
	app.Win.MoveTabToGroup(tab, right, 0)
	app.focusMainGroup(right)
	app.Input.SetInput("retained draft")
	plans := map[string]*activity.Plan{"left": hoverPlanFixture("Left owner goal", 4), "right": hoverPlanFixture("Right owner goal", 18)}
	paintHoverFixture(app, plans)
	screen := app.mainPanes.agentViews["left"].chat
	anchor := app.planActivityRect(screen)
	focus, group, agent := app.rootHost.Focused(), app.Win.ActiveGroup(), app.AgentView.ActiveID()
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: anchor.X + 5, Y: anchor.Y})
	if app.planHover == nil || app.planHover.owner != screen {
		t.Fatal("status hover did not open the exact left pane's plan")
	}
	if app.windowManager.Focused() != nil || app.rootHost.Focused() != focus || app.Win.ActiveGroup() != group || app.AgentView.ActiveID() != agent {
		t.Fatal("passive hover changed keyboard/session/pane ownership")
	}
	if text := hoverPopoverText(app); !strings.Contains(text, "Left owner goal") || strings.Contains(text, "Right owner goal") || !strings.Contains(text, "Verify outcome 04") {
		t.Fatalf("wrong or incomplete plan: %s", text)
	}
	fw := app.overlayHost.Window(ui.OverlayPlanHover)
	x, y := fw.Position()
	w, h := fw.Size()
	if x < 0 || y < 0 || x+w > app.width || y+h > anchor.Y {
		t.Fatal("plan is not bounded above its activity strip")
	}
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: x + 5, Y: y + 2})
	if app.planHover == nil {
		t.Fatal("moving into popover dismissed it")
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if app.planHover != nil || app.Input.Value() != "retained draftx" {
		t.Fatalf("passive hover stole typing: %q", app.Input.Value())
	}
	paintHoverFixture(app, plans)
	anchor = app.planActivityRect(app.mainPanes.agentViews["right"].chat)
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: anchor.X + 5, Y: anchor.Y})
	fw = app.overlayHost.Window(ui.OverlayPlanHover)
	if fw == nil {
		t.Fatal("right plan did not open")
	}
	x, y = fw.Position()
	app.dispatchMouseClick(tea.MouseClickMsg{X: x + 4, Y: y + 2, Button: tea.MouseLeft})
	app.dispatchMouseRelease(tea.MouseReleaseMsg{X: x + 4, Y: y + 2, Button: tea.MouseLeft})
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEnd})
	if text := hoverPopoverText(app); !strings.Contains(text, "Verify outcome 18") {
		t.Fatalf("long plan's last step inaccessible: %q", text)
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.planHover != nil || app.Input.Value() != "retained draftx" {
		t.Fatal("Escape changed draft or left popover open")
	}
}

func TestPlanHoverRetiresOnCompletionUnmountAndModal(t *testing.T) {
	for _, scenario := range []string{"complete", "unmount", "modal", "leave", "outside", "escape", "workspace"} {
		t.Run(scenario, func(t *testing.T) {
			app := newFullTestApp(t)
			app.AddOrchestrator("owner")
			plans := map[string]*activity.Plan{"owner": hoverPlanFixture("Current goal", 3)}
			paintHoverFixture(app, plans)
			anchor := app.planActivityRect(app.chatScreen)
			app.dispatchMouseMotion(tea.MouseMotionMsg{X: anchor.X + 3, Y: anchor.Y})
			if app.planHover == nil {
				t.Fatal("fixture failed to open")
			}
			switch scenario {
			case "complete":
				plans["owner"] = nil
				paintHoverFixture(app, plans)
			case "unmount":
				app.MinimizeAgent("owner")
				app.syncAgentWindowTabs()
				paintHoverFixture(app, plans)
			case "modal":
				app.openHelp()
				app.syncPlanHover()
			case "leave":
				app.dispatchMouseMotion(tea.MouseMotionMsg{X: 0, Y: 0})
			case "outside":
				app.dispatchMouseClick(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
			case "escape":
				app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
			case "workspace":
				app.closeWorkspaceBoundOverlays()
			}
			if app.planHover != nil || app.overlayHost.IsOpen(ui.OverlayPlanHover) {
				t.Fatal("stale plan popover remained")
			}
		})
	}
}

func TestPlanHoverScrollbarKeepsCaptureOutside(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("owner")
	paintHoverFixture(app, map[string]*activity.Plan{"owner": hoverPlanFixture("Scrollable goal", 25)})
	anchor := app.planActivityRect(app.chatScreen)
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: anchor.X + 3, Y: anchor.Y})
	h := app.planHover
	if h == nil {
		t.Fatal("plan did not open")
	}
	_ = hoverPopoverText(app)
	fw := app.overlayHost.Window(ui.OverlayPlanHover)
	x, y := fw.Position()
	r, ok := h.widget.Host().ScreenRect("plan-hover/body/scrollbar")
	if !ok {
		t.Fatal("long plan scrollbar not mounted")
	}
	app.dispatchMouseClick(tea.MouseClickMsg{X: x + r.X, Y: y + r.Y, Button: tea.MouseLeft})
	if !h.widget.Host().HasPointerCapture() {
		t.Fatal("scrollbar did not capture")
	}
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: app.width - 1, Y: app.height - 1, Button: tea.MouseLeft})
	if app.planHover != h {
		t.Fatal("captured scrollbar dismissed on outside motion")
	}
	app.dispatchMouseRelease(tea.MouseReleaseMsg{X: app.width - 1, Y: app.height - 1, Button: tea.MouseLeft})
	if h.widget.Host().HasPointerCapture() || !strings.Contains(hoverPopoverText(app), "Verify outcome 25") {
		t.Fatal("outside release failed to reveal tail or clear capture")
	}
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: app.width - 1, Y: 0})
	if app.planHover != nil {
		t.Fatal("settled outside hover stayed open")
	}
}

func TestPlanHoverRefreshResizeAndAbsentPlan(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("owner")
	paintHoverFixture(app, nil)
	anchor := app.planActivityRect(app.chatScreen)
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: anchor.X + 3, Y: anchor.Y})
	if app.planHover != nil {
		t.Fatal("hover invented a plan")
	}
	p := hoverPlanFixture("Original goal", 3)
	plans := map[string]*activity.Plan{"owner": p}
	paintHoverFixture(app, plans)
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: anchor.X + 3, Y: anchor.Y})
	plans["owner"] = hoverPlanFixture("Replacement goal", 4)
	plans["owner"].Revision = 2
	paintHoverFixture(app, plans)
	if text := hoverPopoverText(app); !strings.Contains(text, "Replacement goal") || strings.Contains(text, "Original goal") {
		t.Fatalf("popover did not follow committed replacement: %q", text)
	}
	app.width, app.height = 64, 24
	paintHoverFixture(app, plans)
	if app.planHover != nil {
		fw := app.overlayHost.Window(ui.OverlayPlanHover)
		x, y := fw.Position()
		w, h := fw.Size()
		if x < 0 || y < 0 || x+w > 64 || y+h > 24 {
			t.Fatal("resize left plan outside the terminal")
		}
	}
	// Captured transcript selection must never summon a hover surface.
	app.clearPlanHover()
	app.rootHost.CapturePointer(app.chatScreen.ID(), tea.MouseLeft)
	anchor = app.planActivityRect(app.chatScreen)
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: anchor.X + 3, Y: anchor.Y, Button: tea.MouseLeft})
	if app.planHover != nil {
		t.Fatal("selection drag summoned a plan")
	}
	app.rootHost.CancelPointerCapture()
}

// The passive plan popover is a reading surface: dragging over its text in the
// real shell selects it and release copies it, without dismissing the popover
// mid-drag or touching the composer draft.
func TestPlanHoverTextIsSelectableAndCopiable(t *testing.T) {
	app := newFullTestApp(t)
	app.Tree.Visible = false
	app.width, app.height = 160, 40
	app.AddOrchestrator("solo")
	app.Input.SetInput("retained draft")
	paintHoverFixture(app, map[string]*activity.Plan{"solo": hoverPlanFixture("Selectable goal", 4)})
	screen := app.mainPanes.agentViews["solo"].chat
	anchor := app.planActivityRect(screen)
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: anchor.X + 5, Y: anchor.Y})
	fw := app.overlayHost.Window(ui.OverlayPlanHover)
	if app.planHover == nil || fw == nil {
		t.Fatal("plan popover did not open")
	}
	x, y := fw.Position()
	w, h := fw.Size()
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: x + 3, Y: y + 1})
	app.dispatchMouseClick(tea.MouseClickMsg{X: x + 2, Y: y + 2, Button: tea.MouseLeft})
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: x + w - 2, Y: y + h - 2, Button: tea.MouseLeft})
	if app.planHover == nil {
		t.Fatal("dragging over the popover dismissed it")
	}
	if !app.planHover.widget.Host().HasPointerCapture() {
		t.Fatal("drag is not captured by the popover")
	}
	_, cmd := app.dispatchMouseRelease(tea.MouseReleaseMsg{X: x + w - 2, Y: y + h - 2, Button: tea.MouseLeft})
	if cmd == nil {
		t.Fatal("releasing a drag over the plan did not copy it")
	}
	if app.planHover == nil || app.Input.Value() != "retained draft" {
		t.Fatalf("selection closed the popover or changed the draft: %q", app.Input.Value())
	}
}

// Connected shells receive a fresh copy of the plan on every frame. Comparing
// pointers treated each copy as a new plan, re-laid out the popover and wiped
// the selection mid-drag, so nothing could be copied in the real product.
func TestPlanHoverSelectionSurvivesFreshPlanCopiesEachFrame(t *testing.T) {
	app := newFullTestApp(t)
	app.Tree.Visible = false
	app.width, app.height = 160, 40
	app.AddOrchestrator("solo")
	original := hoverPlanFixture("Selectable goal", 4)
	frame := func() {
		copied := *original
		copied.Steps = append([]activity.Step(nil), original.Steps...)
		paintHoverFixture(app, map[string]*activity.Plan{"solo": &copied})
	}
	frame()
	screen := app.mainPanes.agentViews["solo"].chat
	anchor := app.planActivityRect(screen)
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: anchor.X + 5, Y: anchor.Y})
	frame()
	fw := app.overlayHost.Window(ui.OverlayPlanHover)
	if app.planHover == nil || fw == nil {
		t.Fatal("plan popover did not open")
	}
	x, y := fw.Position()
	w, h := fw.Size()
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: x + 3, Y: y + 1})
	frame()
	app.dispatchMouseClick(tea.MouseClickMsg{X: x + 2, Y: y + 2, Button: tea.MouseLeft})
	frame()
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: x + w - 2, Y: y + h - 2, Button: tea.MouseLeft})
	frame()
	_, cmd := app.dispatchMouseRelease(tea.MouseReleaseMsg{X: x + w - 2, Y: y + h - 2, Button: tea.MouseLeft})
	if cmd == nil {
		t.Fatal("a drag across per-frame plan copies copied nothing")
	}
	// A genuinely newer revision still replaces the projection.
	newer := hoverPlanFixture("Replacement goal", 4)
	newer.Revision = original.Revision + 1
	paintHoverFixture(app, map[string]*activity.Plan{"solo": newer})
	if text := hoverPopoverText(app); !strings.Contains(text, "Replacement goal") {
		t.Fatalf("newer revision not shown: %q", text)
	}
}
