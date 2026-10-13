package ui

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"crude/types/control"
)

// ControlOperation is semantic intent, never a synthetic keyboard/pointer event.
// Range offsets are logical rune offsets; scroll offsets and sizes are cells.
type ControlOperation struct {
	Verb            string          `json:"verb"`
	Value           json.RawMessage `json:"value,omitempty"`
	ItemID          string          `json:"item_id,omitempty"`
	Offset          int             `json:"offset,omitempty"`
	SecondaryOffset int             `json:"secondary_offset,omitempty"`
	Start           int             `json:"start,omitempty"`
	End             int             `json:"end,omitempty"`
	Fraction        *float64        `json:"fraction,omitempty"`
}

// ControlProvider describes structure, not permission. Effects of emitted owner
// actions must be resolved and authorized by the caller before Control is used.
type ControlProvider interface {
	ControlActions() []control.Descriptor
	HandleControl(EventContext, ControlOperation) (EventResult, error)
}

// ControlChildrenProvider exposes manually painted controls without changing
// the Host's human hit-testing or painting contracts.
type ControlChildrenProvider interface{ ControlChildren() []Component }

type controlMount struct {
	component       Component
	parent          ComponentID
	owner           ComponentID
	bounds          Rect
	epoch, revision uint64
	digest          [32]byte
}

var controlLifetimeSequence atomic.Uint64

// ActionBar retains and paints buttons manually; expose those same owners.
func (ab *ActionBar) ControlChildren() []Component {
	out := make([]Component, 0, len(ab.buttons))
	for _, button := range ab.buttons {
		out = append(out, button.button)
	}
	return out
}

// ControlLifetime is an allocation identity, never an address vulnerable to reuse.
func (h *Host) ControlLifetime() uint64 {
	h.Layout()
	h.syncControlMounts(false)
	if h.root == nil {
		return 0
	}
	return h.controls[h.root.ID()].epoch
}

func controlError(code control.FaultCode, message string) error {
	return &control.Fault{Code: code, Message: message}
}
func unsupportedControl() (EventResult, error) {
	return EventResult{}, controlError(control.Invalid, "unsupported semantic operation")
}
func controlResult(action string, payload any) EventResult {
	r := EventResult{Handled: true, Invalidate: InvalidateLayout | InvalidatePaint}
	if action != "" {
		r.Action = &Action{ID: action, Payload: payload}
	}
	return r
}
func controlActions(enabled bool, ids ...string) []control.Descriptor {
	out := make([]control.Descriptor, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		availability := control.Availability{Available: enabled}
		if !enabled {
			availability.Reason, availability.Message = control.TargetUnavailable, "control is disabled"
		}
		out = append(out, control.Descriptor{ID: id, Scope: control.Shell, Input: control.Schema{ID: "ui.control.operation", Revision: 1, Document: json.RawMessage(`{"type":"object"}`)}, Output: control.Schema{ID: "ui.control.result", Revision: 1, Document: json.RawMessage(`{"type":"object"}`)}, Requires: control.Requirements{OpenEpoch: true, Revision: true}, Availability: availability})
	}
	// No generic action claims a read effect. Contextual owners resolve effects,
	// HumanOnly and schemas; empty Effect deliberately fails grant validation.
	return out
}

func (h *Host) syncControlMounts(refresh bool) {
	old := h.controls
	next := h.controlScratch
	if next == nil {
		next = make(map[ComponentID]*controlMount, len(old))
	} else {
		clear(next)
	}
	var walk func(Component, ComponentID, ComponentID, int, int)
	walk = func(c Component, parent, owner ComponentID, ox, oy int) {
		if c == nil {
			return
		}
		b := c.Bounds()
		b.X += ox
		b.Y += oy
		if _, ok := next[c.ID()]; ok {
			return
		}
		if _, ok := h.registry[c.ID()]; ok {
			owner = c.ID()
			b = h.screen[c.ID()]
		}
		m := old[c.ID()]
		if m == nil || m.component != c {
			m = &controlMount{component: c}
			h.controlEpoch = controlLifetimeSequence.Add(1)
			m.epoch = h.controlEpoch
		}
		m.parent, m.owner, m.bounds = parent, owner, b
		if refresh {
			digest := controlDigest(c)
			// A contextual draft owner fences retained actions (including Save)
			// against edits in sibling/off-page controls and unobserved ABA.
			for ancestor := parent; ancestor != RootID; {
				mounted := next[ancestor]
				if mounted == nil {
					break
				}
				if source, ok := mounted.component.(interface{ ControlEditGeneration() uint64 }); ok {
					digest = sha256.Sum256([]byte(fmt.Sprintf("%x:%d", digest, source.ControlEditGeneration())))
				}
				ancestor = mounted.parent
			}
			if m.revision == 0 || digest != m.digest {
				m.revision++
				m.digest = digest
			}
		}
		next[c.ID()] = m
		if p, ok := c.(Parent); ok {
			for _, ch := range p.Children() {
				walk(ch, c.ID(), owner, b.X, b.Y)
			}
		}
		if p, ok := c.(ControlChildrenProvider); ok {
			for _, ch := range p.ControlChildren() {
				walk(ch, c.ID(), owner, b.X, b.Y)
			}
		}
	}
	walk(h.root, RootID, RootID, 0, 0)
	h.controls = next
	h.controlScratch = old
}

func controlDigest(c Component) [32]byte {
	s := c.Semantics()
	// Private source bytes participate only in the digest, never inspection.
	if t, ok := c.(TextClipboardTarget); ok && t.TextSecret() {
		s.Value = t.TextValue()
	}
	if f, ok := c.(*FormField); ok && f.field.Type == FieldPassword {
		s.Value = f.field.Value.String()
	}
	// Focus/hover/paint and geometry do not participate in source revisions.
	data := struct {
		Semantics      Semantics
		Source         any
		Actions        []control.Descriptor
		EditGeneration uint64
	}{Semantics: s, Source: controlSource(c)}
	if f, ok := c.(*FormField); ok && f.model != nil {
		data.EditGeneration = f.model.EditGeneration()
	}
	if f, ok := c.(*Form); ok && f.model != nil {
		data.EditGeneration = f.model.EditGeneration()
	}
	if p, ok := c.(ControlProvider); ok {
		data.Actions = p.ControlActions()
	}
	raw, _ := json.Marshal(data)
	return sha256.Sum256(raw)
}

// InspectControls returns a bounded flat page, including offscreen collection
// items. Cursors fence the complete source inventory, not frame/focus changes.
func (h *Host) InspectControls(target control.Target, req control.PageRequest) (control.Page, error) {
	return h.InspectControlsProjected(target, req, control.MaxValueBytes, nil)
}

// InspectControlsProjected retains the Host's source cursor while a containing
// shell qualifies/redacts nodes and imposes its remaining response-byte budget.
// Projection changes metadata only; it cannot enumerate, drop or reorder source
// records. The first record that does not fit remains at the next cursor.
func (h *Host) InspectControlsProjected(target control.Target, req control.PageRequest, maxBytes int, project func(control.Node) control.Node) (control.Page, error) {
	if req.Limit < 1 || req.Limit > control.MaxPageLimit {
		return control.Page{}, controlError(control.Invalid, "limit must be 1..128")
	}
	if maxBytes < 1024 || maxBytes > control.MaxValueBytes {
		return control.Page{}, controlError(control.Invalid, "inspection byte budget must be 1024..65536")
	}
	h.Layout()
	h.syncControlMounts(true)
	ids := make([]ComponentID, 0, len(h.controls))
	for id := range h.controls {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	hash := sha256.New()
	total := 0
	for _, id := range ids {
		m := h.controls[id]
		fmt.Fprintf(hash, "%s:%d:%d;", id, m.epoch, m.revision)
		total += 1 + controlItemCount(m.component)
	}
	stamp := fmt.Sprintf("%x", hash.Sum(nil))
	start := 0
	if req.Cursor != "" {
		parts := strings.Split(req.Cursor, ":")
		if len(parts) != 2 || parts[0] != stamp {
			return control.Page{}, controlError(control.Stale, "inspection inventory changed")
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 0 || n >= total {
			return control.Page{}, controlError(control.Invalid, "invalid cursor")
		}
		start = n
	}
	page := control.Page{Nodes: make([]control.Node, 0, req.Limit)}
	position := 0
	bytes := 0
	for _, id := range ids {
		m := h.controls[id]
		count := 1 + controlItemCount(m.component)
		if position+count <= start {
			position += count
			continue
		}
		for i := max(0, start-position); i < count; i++ {
			if len(page.Nodes) >= req.Limit {
				page.NextCursor = stamp + ":" + strconv.Itoa(position+i)
				return page, nil
			}
			node := h.controlNode(id, m, target)
			if i > 0 {
				node = controlItemNode(m.component, i-1, node)
			}
			if project != nil {
				node = project(node)
			}
			raw, err := json.Marshal(node)
			if err != nil {
				return control.Page{}, err
			}
			if len(raw) > maxBytes-1024 {
				return control.Page{}, controlError(control.Invalid, "single control exceeds inspection budget")
			}
			if bytes+len(raw)+len(page.Nodes) > maxBytes-1024 {
				page.NextCursor = stamp + ":" + strconv.Itoa(position+i)
				return page, nil
			}
			bytes += len(raw)
			page.Nodes = append(page.Nodes, node)
		}
		position += count
	}
	return page, nil
}

// InspectControl addresses a mounted control directly. Offscreen collection
// records are not mounts; their actions target the collection and exact ItemID.
// Source synchronization is identical to paged inspection, without serializing
// every preceding collection item to reach a later component.
func (h *Host) InspectControl(id ComponentID, target control.Target) (control.Node, error) {
	h.Layout()
	h.syncControlMounts(true)
	m := h.controls[id]
	if m == nil {
		return control.Node{}, controlError(control.Unavailable, "control is not mounted")
	}
	return h.controlNode(id, m, target), nil
}
func (h *Host) controlNode(id ComponentID, m *controlMount, target control.Target) control.Node {
	s := m.component.Semantics()
	if f, ok := m.component.(*FormField); ok {
		s = Semantics{Role: RoleTextInput, Label: f.field.Label, Disabled: f.field.Disabled}
		if f.field.Type == FieldSeparator || f.field.Type == FieldSpacer {
			s.Role = RoleLabel
		}
	}
	r := m.bounds
	n := control.Node{ID: string(id), ParentID: string(m.parent), Name: s.Label, Role: controlRole(s.Role), Target: target, OpenEpoch: m.epoch, Revision: m.revision, State: control.NodeState{Enabled: !s.Disabled, Visible: !intersectRect(r, Rect{W: h.width, H: h.height}).Empty(), Focused: h.focused == m.owner, Selected: s.Selected}, Bounds: &control.Bounds{X: r.X, Y: r.Y, Width: max(0, r.W), Height: max(0, r.H)}}
	value := controlValue(m.component)
	if value != nil {
		raw, _ := json.Marshal(value)
		if len(raw) <= control.MaxValueBytes {
			n.Value = raw
		}
	}
	if p, ok := m.component.(ControlProvider); ok {
		n.Actions = p.ControlActions()
	}
	return n
}
func controlRole(r SemanticRole) control.Role {
	switch r {
	case RoleButton:
		return control.Button
	case RoleTextInput:
		return control.Field
	case RoleList:
		return control.List
	case RoleTree:
		return control.Tree
	case RoleListItem, RoleMenuItem:
		return control.Item
	case RoleDialog:
		return control.Window
	case RoleLabel:
		return control.Text
	case RoleScrollBar:
		return control.Scrollbar
	case RoleSeparator:
		return control.Divider
	default:
		return control.Container
	}
}

// Control runs on the UI owner goroutine. Callers must authorize the exact
// underlying action ID, and must enforce overlay/deck policy between Hosts.
func (h *Host) Control(id ComponentID, epoch uint64, revision *uint64, op ControlOperation) (EventResult, error) {
	h.Layout()
	h.syncControlMounts(true)
	m := h.controls[id]
	if m == nil {
		return EventResult{}, controlError(control.Unavailable, "control is not mounted")
	}
	if epoch == 0 || epoch != m.epoch || (revision != nil && *revision != m.revision) {
		return EventResult{}, controlError(control.Stale, "control lifetime or revision changed")
	}
	if h.FocusScopeModal() && !h.FocusWithinScope(m.owner) {
		return EventResult{}, controlError(control.Denied, "control is outside modal scope")
	}
	if h.HasPointerCapture() && h.captured != m.owner {
		return EventResult{}, controlError(control.Denied, "another control owns pointer capture")
	}
	if m.component.Semantics().Disabled {
		return EventResult{}, controlError(control.Unavailable, "control is disabled")
	}
	p, ok := m.component.(ControlProvider)
	if !ok {
		return unsupportedControl()
	}
	if len(op.Value) > control.MaxValueBytes {
		return EventResult{}, controlError(control.Invalid, "value exceeds budget")
	}
	// An open form dropdown owns input until committed/dismissed.
	for _, mounted := range h.controls {
		if form, ok := mounted.component.(*Form); ok {
			for _, field := range form.fields {
				if field.selectOpen && field.ID() != id {
					return EventResult{}, controlError(control.Denied, "another form dropdown owns input")
				}
			}
		}
	}
	h.Focus(m.owner)
	if d, ok := m.component.(*SplitDivider); ok && op.Fraction != nil && d.model != nil {
		parent := h.controls[m.parent]
		if parent == nil {
			return EventResult{}, controlError(control.Unavailable, "fraction resize requires mounted parent extent")
		}
		op.SecondaryOffset = parent.bounds.W
		if d.model.Dir() == Vertical {
			op.SecondaryOffset = parent.bounds.H
		}
	}
	if f, ok := m.component.(*FormField); ok && m.owner != id {
		if form, ok := h.registry[m.owner].(*Form); ok {
			for index, field := range form.fields {
				if field == f {
					form.focused = index
					break
				}
			}
		}
	}
	ctx := EventContext{Theme: h.theme, Focused: true, ScreenX: m.bounds.X, ScreenY: m.bounds.Y}
	res, err := p.HandleControl(ctx, op)
	if err != nil {
		return res, err
	}
	// Manual composites must receive the same action translation as mounted ones.
	if m.owner != id {
		if owner, ok := h.registry[m.owner].(ActionHandler); ok && res.Action != nil {
			reply := owner.HandleAction(ctx, *res.Action)
			if reply.Handled {
				res = res.Merge(reply)
				res.Action = reply.Action
			}
		}
	}
	res = h.bubbleAction(m.owner, ctx.ScreenX, ctx.ScreenY, res)
	if res.Invalidate&InvalidateLayout != 0 {
		h.MarkLayout()
	} else if res.Invalidate != 0 {
		h.MarkPaint()
	}
	if res.Action != nil && res.Action.ID == "ui.focus" {
		if focus, ok := res.Action.Payload.(ComponentID); ok && h.Focus(focus) {
			res.Action = nil
		}
	}
	h.syncControlMounts(true)
	return res, nil
}

// DescribeControl resolves the semantic verb to its emitted owner action ID
// without applying it. Unresolved owner actions intentionally have no Effect;
// the shell's contextual registry must resolve authority before admission.
// Pure selection, scrolling and draft edits are shell-local Write operations.
func (h *Host) DescribeControl(id ComponentID, op ControlOperation) (control.Descriptor, error) {
	h.Layout()
	h.syncControlMounts(true)
	m := h.controls[id]
	if m == nil {
		return control.Descriptor{}, controlError(control.Unavailable, "control is not mounted")
	}
	action := ""
	local := false
	switch c := m.component.(type) {
	case *Button:
		if op.Verb == "activate" {
			action = c.actionID
		}
	case *ToggleButton:
		if op.Verb == "activate" || op.Verb == "set_checked" {
			action = c.actionID
		}
	case *List, *Menu, *Table, *Tree, *Tabs, *VirtualList:
		if op.Verb == "activate" || op.Verb == "select" || op.Verb == "expand" || op.Verb == "close" {
			found := false
			for i := 0; i < controlItemCount(c); i++ {
				item := controlItemAt(c, i)
				if item.ID == op.ItemID {
					if found {
						return control.Descriptor{}, controlError(control.Invalid, "ambiguous item")
					}
					found = true
					if !item.Enabled {
						return control.Descriptor{}, controlError(control.Unavailable, "item disabled")
					}
				}
			}
			if !found {
				return control.Descriptor{}, controlError(control.Unavailable, "item unavailable")
			}
		}
		switch c := c.(type) {
		case *List:
			if op.Verb == "activate" {
				action = c.activate
			}
		case *Menu:
			if op.Verb == "activate" {
				action = c.activate
			}
		case *Table:
			if op.Verb == "activate" {
				action = c.activate
			}
		case *Tree:
			switch op.Verb {
			case "activate":
				action = c.activate
			case "expand":
				action = c.toggle
			case "select":
				action = "tree.select"
			}
		case *Tabs:
			if op.Verb == "activate" || op.Verb == "select" {
				action = "tabs.activate"
			}
			if op.Verb == "close" {
				action = "tabs.close"
			}
		case *VirtualList:
			if c.pointerPolicy == PointerPolicyPassthrough {
				return control.Descriptor{}, controlError(control.Unavailable, "collection interaction is screen owned")
			}
			if op.Verb == "activate" {
				action = c.activate
			}
		}
		if action == "" && (op.Verb == "select" || op.Verb == "scroll" || op.Verb == "set_offset") {
			local = true
		}
	case *FormField:
		if c.field.skipFocus() || c.field.ReadOnly {
			return control.Descriptor{}, controlError(control.Unavailable, "field unavailable")
		}
		if op.Verb == "activate" && c.field.Type == FieldNav {
			action = c.field.Target
		} else if op.Verb == "activate" {
			if c.control != nil {
				action = c.control.submitID
			}
			if c.area != nil {
				action = c.area.submitID
			}
		} else {
			switch op.Verb {
			case "set_value", "select", "set_checked", "replace", "undo", "redo", "append", "set", "remove":
				action = "form.changed"
			case "expand":
				action = "form.select.open"
			case "select_range", "dismiss":
				local = true
			}
		}
	case *TextField:
		if op.Verb == "activate" {
			action = c.submitID
		} else {
			local = controlTextVerb(op.Verb) && (!c.disabled && (c.TextEditable() || op.Verb == "select_range"))
		}
	case *TextArea:
		if op.Verb == "activate" {
			action = c.submitID
		} else {
			local = controlTextVerb(op.Verb) && (c.TextEditable() || op.Verb == "select_range")
		}
	case *FilterField, *SearchField:
		local = controlTextVerb(op.Verb)
	case *Form, *ScrollbarControl, *Viewport:
		local = op.Verb == "set_offset" || op.Verb == "scroll"
	case *SplitDivider:
		if op.Verb == "resize" {
			action = ActionSplitResize
		}
	default:
		if p, ok := c.(ControlProvider); ok {
			for _, d := range p.ControlActions() {
				if d.ID == op.Verb {
					return d, nil
				}
			}
		}
	}
	if action == "" && !local {
		return control.Descriptor{}, controlError(control.Invalid, "unsupported semantic operation")
	}
	if local {
		action = "ui.control." + op.Verb
	}
	d := controlActions(!m.component.Semantics().Disabled, action)[0]
	if local {
		d.Effect = control.Write
	}
	return d, nil
}
func controlTextVerb(verb string) bool {
	switch verb {
	case "set_value", "replace", "undo", "redo", "select_range":
		return true
	}
	return false
}
