package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

// parentCatcher handles keys its children leave unhandled — used to prove Host bubbling.
type parentCatcher struct {
	Base
	child   Component
	gotKeys []string
}

func (p *parentCatcher) Children() []Component { return []Component{p.child} }
func (p *parentCatcher) Handle(_ EventContext, evt Event) EventResult {
	if !evt.IsKey() {
		return EventResult{}
	}
	p.gotKeys = append(p.gotKeys, evt.Key.String())
	return EventResult{Handled: true, Invalidate: InvalidatePaint}
}

func TestHost_KeyBubbling_UnhandledBubblesToParent(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	field := NewTextField("root/field", tokens)
	parent := &parentCatcher{Base: NewBase("root"), child: field}
	host := NewHost(th, parent)
	host.RegisterTree(parent)
	host.Resize(40, 10)
	host.Layout()
	host.Focus(field.ID())

	res := host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	if !res.Handled {
		t.Fatal("expected parent to handle bubbled Down")
	}
	if len(parent.gotKeys) != 1 || parent.gotKeys[0] != "down" {
		t.Fatalf("parent gotKeys=%v want [down]", parent.gotKeys)
	}
}

func TestHost_KeyBubbling_HandledKeysDoNotBubble(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	field := NewTextField("root/field", tokens)
	parent := &parentCatcher{Base: NewBase("root"), child: field}
	host := NewHost(th, parent)
	host.RegisterTree(parent)
	host.Resize(40, 10)
	host.Layout()
	host.Focus(field.ID())

	res := host.Handle(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !res.Handled {
		t.Fatal("text field should handle printable")
	}
	if field.Value() != "x" {
		t.Fatalf("value=%q", field.Value())
	}
	if len(parent.gotKeys) != 0 {
		t.Fatalf("printable must not bubble; parent got %v", parent.gotKeys)
	}
}

func TestHost_KeyBubbling_StopsAtFirstHandler(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	field := NewTextField("root/mid/field", tokens)
	mid := &parentCatcher{Base: NewBase("root/mid"), child: field}
	root := &parentCatcher{Base: NewBase("root"), child: mid}
	host := NewHost(th, root)
	host.RegisterTree(root)
	host.Resize(40, 10)
	host.Layout()
	host.Focus(field.ID())

	res := host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
	if !res.Handled {
		t.Fatal("expected mid to handle bubbled Up")
	}
	if len(mid.gotKeys) != 1 {
		t.Fatalf("mid gotKeys=%v", mid.gotKeys)
	}
	if len(root.gotKeys) != 0 {
		t.Fatalf("root must not see key after mid handled it; got %v", root.gotKeys)
	}
}

func TestHost_KeyBubbling_Table(t *testing.T) {
	cases := []struct {
		name      string
		msg       tea.KeyPressMsg
		wantBubble bool
		setup     func(*TextField)
	}{
		{name: "up", msg: tea.KeyPressMsg{Code: tea.KeyUp}, wantBubble: true},
		{name: "down", msg: tea.KeyPressMsg{Code: tea.KeyDown}, wantBubble: true},
		{name: "printable", msg: tea.KeyPressMsg{Code: 'a', Text: "a"}, wantBubble: false},
		{name: "backspace", msg: tea.KeyPressMsg{Code: tea.KeyBackspace}, wantBubble: false},
		{name: "left", msg: tea.KeyPressMsg{Code: tea.KeyLeft}, wantBubble: false},
		{
			name: "esc-clears-selection-only",
			msg:  tea.KeyPressMsg{Code: tea.KeyEsc},
			setup: func(f *TextField) {
				f.SetValue("hi")
				f.Edit().SelectAll()
			},
			wantBubble: false,
		},
		{name: "esc-empty", msg: tea.KeyPressMsg{Code: tea.KeyEsc}, wantBubble: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			th := theme.DefaultDark()
			tokens := NewTokenSet(th)
			field := NewTextField("root/field", tokens)
			if tc.setup != nil {
				tc.setup(field)
			}
			parent := &parentCatcher{Base: NewBase("root"), child: field}
			host := NewHost(th, parent)
			host.RegisterTree(parent)
			host.Resize(40, 10)
			host.Layout()
			host.Focus(field.ID())
			_ = host.Handle(tc.msg)
			bubbled := len(parent.gotKeys) > 0
			if bubbled != tc.wantBubble {
				t.Fatalf("bubbled=%v want %v (keys=%v)", bubbled, tc.wantBubble, parent.gotKeys)
			}
		})
	}
}
