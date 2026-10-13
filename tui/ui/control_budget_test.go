package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/types/control"
)

func TestControlBudgetLayoutDoesNotObserveUnrequestedControls(t *testing.T) {
	h := controlTestHost(NewButton("button", "Run", "run"))
	buf := surface.NewCellBuffer(50, 10)
	for range 3 {
		h.MarkLayout()
		h.Layout()
		h.Paint(buf)
	}
	if len(h.controls) != 0 {
		t.Fatal("ordinary human layout/paint allocated semantic inventory")
	}
	if _, err := h.InspectControl("button", control.Target{}); err != nil || len(h.controls) == 0 {
		t.Fatal("explicit observation did not synchronize mounted controls", err)
	}
}

func TestControlBudgetProjectedPagesRetainEveryRecord(t *testing.T) {
	list := NewList("list", nil)
	items := make([]ListItem, 700)
	for i := range items {
		items[i] = ListItem{ID: fmt.Sprintf("%04d", i), Label: strings.Repeat("雪", 120) + fmt.Sprint(i)}
	}
	list.SetItems(items)
	h := controlTestHost(list)
	prefix := strings.Repeat("qualified/", 100)
	project := func(n control.Node) control.Node {
		n.ID = prefix + n.ID
		n.ParentID = prefix + n.ParentID
		n.Actions = nil // Real shells qualify and redact before enforcing the budget.
		return n
	}
	seen := make(map[string]bool)
	cursor, firstCursor := "", ""
	pages := 0
	for {
		page, err := h.InspectControlsProjected(control.Target{WorkspaceID: "exact-workspace"}, control.PageRequest{Limit: 128, Cursor: cursor}, 64*1024, project)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(page)
		if err != nil || len(raw) > 64*1024 {
			t.Fatalf("page exceeds result output contract: %d bytes, %v", len(raw), err)
		}
		if fault := (control.Result{RequestID: "page", OperationID: "page", State: control.Applied, Output: raw}).Validate(); fault != nil {
			t.Fatalf("page cannot pass the actual reply contract: %v", fault)
		}
		if len(page.Nodes) == 0 || (page.NextCursor != "" && page.NextCursor == cursor) {
			t.Fatal("inspection made no progress")
		}
		for _, n := range page.Nodes {
			if seen[n.ID] || n.Target.WorkspaceID != "exact-workspace" {
				t.Fatalf("duplicate or retargeted record: %s", n.ID)
			}
			seen[n.ID] = true
		}
		pages++
		if pages > len(items)+1 {
			t.Fatal("inspection cursor loop")
		}
		if firstCursor == "" {
			firstCursor = page.NextCursor
			if len(page.Nodes) >= 128 || firstCursor == "" {
				t.Fatal("fixture did not exercise byte-limited paging")
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if !seen[prefix+"list"] || len(seen) != len(items)+1 {
		t.Fatalf("lost records: got %d, want %d", len(seen), len(items)+1)
	}
	for _, item := range items {
		if !seen[prefix+"list/item/"+item.ID] {
			t.Fatal("lost exact item", item.ID)
		}
	}
	items[699].Label = "changed offscreen source"
	list.SetItems(items)
	_, err := h.InspectControlsProjected(control.Target{}, control.PageRequest{Limit: 128, Cursor: firstCursor}, 64*1024, project)
	var fault *control.Fault
	if !errors.As(err, &fault) || fault.Code != control.Stale {
		t.Fatalf("changed source accepted old cursor: %v", err)
	}
}

func TestControlBudgetSingleOversizedRecordFailsExplicitly(t *testing.T) {
	h := controlTestHost(NewButton("button", strings.Repeat("x", 64*1024), "run"))
	page, err := h.InspectControls(control.Target{}, control.PageRequest{Limit: 128})
	var fault *control.Fault
	if !errors.As(err, &fault) || fault.Code != control.Invalid || !strings.Contains(fault.Message, "single control") || len(page.Nodes) != 0 || page.NextCursor != "" {
		t.Fatalf("oversized control silently omitted or looped: %+v %v", page, err)
	}
}

type budgetControlParent struct {
	Base
	children       []Component
	removeOnAction bool
}

func (p *budgetControlParent) Children() []Component { return p.children }
func (p *budgetControlParent) Layout(ctx LayoutContext, r Rect) {
	p.SetBounds(r)
	for i, child := range p.children {
		child.Layout(ctx, Rect{Y: i, W: r.W, H: 1})
	}
}
func (p *budgetControlParent) HandleAction(_ EventContext, a Action) EventResult {
	if p.removeOnAction && a.ID == "hide" {
		p.children = nil
		return EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}
	}
	return EventResult{}
}

func TestControlBudgetDirectMountedTailBeyond4096Items(t *testing.T) {
	list := NewVirtualList("a-list", nil)
	items := make([]VirtItem, 5000)
	for i := range items {
		items[i] = VirtItem{ID: fmt.Sprint(i), Label: "row", Height: 1}
	}
	list.SetItems(items)
	button := NewButton("z-button", "Run", "exact.tail.run")
	h := controlTestHost(&budgetControlParent{Base: NewBase("root"), children: []Component{list, button}})
	n, err := h.InspectControl(button.ID(), control.Target{WorkspaceID: "w"})
	if err != nil || n.ID != "z-button" || n.OpenEpoch == 0 {
		t.Fatalf("mounted tail inaccessible: %+v %v", n, err)
	}
	d, err := h.DescribeControl(button.ID(), ControlOperation{Verb: "activate"})
	if err != nil || d.ID != "exact.tail.run" {
		t.Fatalf("tail description: %+v %v", d, err)
	}
	res, err := h.Control(button.ID(), n.OpenEpoch, &n.Revision, ControlOperation{Verb: "activate"})
	if err != nil || res.Action == nil || res.Action.ID != "exact.tail.run" {
		t.Fatalf("tail action: %+v %v", res, err)
	}
}

func TestControlBudgetHumanUnmountInvalidatesLifetimeWithoutDiscardingDraft(t *testing.T) {
	button := NewButton("button", "Hide", "hide")
	draft := NewTextArea("draft", nil, true)
	draft.SetValue("retained unfinished work")
	p := &budgetControlParent{Base: NewBase("root"), children: []Component{button, draft}, removeOnAction: true}
	h := controlTestHost(p)
	old, err := h.InspectControl(button.ID(), control.Target{})
	if err != nil {
		t.Fatal(err)
	}
	h.Focus(button.ID())
	if res := h.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); !res.Handled || len(p.children) != 0 {
		t.Fatal("human action did not unmount controls")
	}
	h.Layout() // No semantic observation during the hidden interval.
	p.children = []Component{button, draft}
	h.MarkLayout()
	current, err := h.InspectControl(button.ID(), control.Target{})
	if err != nil || current.OpenEpoch == old.OpenEpoch || draft.Value() != "retained unfinished work" {
		t.Fatalf("human hide/reopen lost lifetime or draft: %+v %v", current, err)
	}
	_, err = h.Control(button.ID(), old.OpenEpoch, &old.Revision, ControlOperation{Verb: "activate"})
	var fault *control.Fault
	if !errors.As(err, &fault) || fault.Code != control.Stale || len(p.children) != 2 {
		t.Fatalf("stale action affected reopened owner: %v", err)
	}
	p.children[0] = NewButton("button", "Replacement", "hide")
	h.MarkLayout()
	replacement, err := h.InspectControl(button.ID(), control.Target{})
	if err != nil || replacement.OpenEpoch == current.OpenEpoch || draft.Value() != "retained unfinished work" {
		t.Fatalf("replacement reused lifetime or discarded draft: %+v %v", replacement, err)
	}
}
