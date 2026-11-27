//go:build linux && !android

package clipboard

import (
	"bytes"
	"errors"
	"io"
	"os"

	"github.com/jezek/xgb"
	x "github.com/jezek/xgb/xproto"
)

// The X helper owns a single hidden window. ICCCM 2.7 requires a handshake
// per property chunk, since one X request cannot carry an arbitrary selection.
// https://www.x.org/releases/current/doc/xorg-docs/icccm/icccm.html
// XGB owns authentication, framing, event decoding and the connection lifetime.
type xSelection struct {
	c     *xgb.Conn
	w     x.Window
	atoms map[string]x.Atom
	chunk int
}

func runXSelection(action string) error {
	c, err := xgb.NewConn()
	if err != nil {
		return err
	}
	defer c.Close()
	setup := x.Setup(c)
	w, err := x.NewWindowId(c)
	if err != nil {
		return err
	}
	if err = x.CreateWindowChecked(c, 0, w, setup.DefaultScreen(c).Root, 0, 0, 1, 1, 0, x.WindowClassInputOnly, 0, x.CwEventMask, []uint32{x.EventMaskPropertyChange}).Check(); err != nil {
		return err
	}
	s := xSelection{c: c, w: w, chunk: 4*int(setup.MaximumRequestLength) - 24, atoms: map[string]x.Atom{}}
	if s.chunk < 4 {
		return errUnavailable
	}
	for _, name := range []string{"CLIPBOARD", "TARGETS", "INCR", "CRUDE_SELECTION", "UTF8_STRING", "text/plain;charset=utf-8", "text/plain", "image/png", "image/jpeg"} {
		a, e := x.InternAtom(c, false, uint16(len(name)), name).Reply()
		if e != nil {
			return e
		}
		s.atoms[name] = a.Atom
	}
	if action == "copy" {
		data, e := io.ReadAll(os.Stdin)
		if e != nil {
			return e
		}
		return s.own(data)
	}
	owner, err := x.GetSelectionOwner(c, s.atoms["CLIPBOARD"]).Reply()
	if err != nil || owner.Owner == 0 {
		return err
	}
	targets := []string{"UTF8_STRING", "text/plain;charset=utf-8", "text/plain"}
	if action == "image" {
		targets = []string{"image/png", "image/jpeg"}
	}
	for _, name := range targets {
		data, ok, e := s.read(s.atoms[name])
		if e != nil {
			return e
		}
		if ok {
			_, e = os.Stdout.Write(data)
			return e
		}
	}
	if action == "text" {
		data, ok, e := s.read(x.AtomString)
		if e != nil {
			return e
		}
		if ok {
			r := make([]rune, len(data))
			for i, b := range data {
				r[i] = rune(b)
			}
			_, e = io.WriteString(os.Stdout, string(r))
			return e
		}
	}
	return nil
}

func (s xSelection) read(target x.Atom) ([]byte, bool, error) {
	prop := s.atoms["CRUDE_SELECTION"]
	sel := s.atoms["CLIPBOARD"]
	if err := x.ConvertSelectionChecked(s.c, s.w, sel, target, prop, x.TimeCurrentTime).Check(); err != nil {
		return nil, false, err
	}
	var b bytes.Buffer
	incremental := false
	for {
		event, e := s.c.WaitForEvent()
		if e != nil {
			return nil, false, e
		}
		if event == nil {
			return nil, false, io.ErrUnexpectedEOF
		}
		switch v := event.(type) {
		case x.SelectionNotifyEvent:
			if incremental || v.Requestor != s.w || v.Selection != sel || v.Target != target {
				continue
			}
			if v.Property != prop {
				return nil, false, nil
			}
		case x.PropertyNotifyEvent:
			if !incremental || v.Window != s.w || v.Atom != prop || v.State != x.PropertyNewValue {
				continue
			}
		default:
			continue
		}
		p, err := x.GetProperty(s.c, true, s.w, prop, x.AtomAny, 0, ^uint32(0)).Reply()
		if err != nil {
			return nil, false, err
		}
		if !incremental && p.Type == s.atoms["INCR"] {
			incremental = true
			continue
		}
		if p.Format != 8 || p.Type != target || p.BytesAfter != 0 {
			return nil, false, errors.New("invalid clipboard property")
		}
		if !incremental {
			return p.Value, true, nil
		}
		if len(p.Value) == 0 {
			return b.Bytes(), true, nil
		}
		b.Write(p.Value)
	}
}

type selectionKey struct {
	win  x.Window
	prop x.Atom
}
type selectionTransfer struct {
	kind x.Atom
	data []byte
}

func (s xSelection) put(k selectionKey, kind x.Atom, width byte, data []byte) error {
	return x.ChangePropertyChecked(s.c, x.PropModeReplace, k.win, k.prop, kind, width, uint32(len(data)/(int(width)/8)), data).Check()
}
func (s xSelection) own(data []byte) error {
	sel := s.atoms["CLIPBOARD"]
	if err := x.SetSelectionOwnerChecked(s.c, s.w, sel, x.TimeCurrentTime).Check(); err != nil {
		return err
	}
	owner, err := x.GetSelectionOwner(s.c, sel).Reply()
	if err != nil {
		return err
	}
	if owner.Owner != s.w {
		return errUnavailable
	}
	if _, err = os.Stdout.Write([]byte{'+'}); err != nil {
		return err
	}
	_ = os.Stdout.Close()
	formats := map[x.Atom][]byte{}
	for _, name := range []string{"UTF8_STRING", "text/plain;charset=utf-8", "text/plain"} {
		formats[s.atoms[name]] = data
	}
	var latin []byte
	latinOK := true
	for _, r := range string(data) {
		if r > 255 {
			latinOK = false
			break
		}
		latin = append(latin, byte(r))
	}
	if latinOK {
		formats[x.AtomString] = latin
	}
	targets := s.atoms["TARGETS"]
	atoms := make([]byte, 4*(1+len(formats)))
	xgb.Put32(atoms, uint32(targets))
	i := 4
	for a := range formats {
		xgb.Put32(atoms[i:], uint32(a))
		i += 4
	}
	transfers := map[selectionKey]selectionTransfer{}
	for {
		ev, e := s.c.WaitForEvent()
		if e != nil {
			return e
		}
		switch v := ev.(type) {
		case nil:
			return io.ErrUnexpectedEOF
		case x.SelectionClearEvent:
			return nil
		case x.DestroyNotifyEvent:
			for k := range transfers {
				if k.win == v.Window {
					delete(transfers, k)
				}
			}
		case x.PropertyNotifyEvent:
			k := selectionKey{v.Window, v.Atom}
			t, ok := transfers[k]
			if !ok || v.State != x.PropertyDelete {
				continue
			}
			n := min(len(t.data), s.chunk)
			if err := s.put(k, t.kind, 8, t.data[:n]); err != nil || n == 0 {
				delete(transfers, k)
			} else {
				t.data = t.data[n:]
				transfers[k] = t
			}
		case x.SelectionRequestEvent:
			prop := v.Property
			if prop == x.AtomNone {
				prop = v.Target
			}
			k := selectionKey{v.Requestor, prop}
			notify := x.SelectionNotifyEvent{Time: v.Time, Requestor: v.Requestor, Selection: v.Selection, Target: v.Target}
			payload, ok := formats[v.Target]
			switch {
			case v.Selection != sel: // refuse a different selection
			case v.Target == targets:
				if s.put(k, x.AtomAtom, 32, atoms) == nil {
					notify.Property = prop
				}
			case ok && len(payload) <= s.chunk:
				if s.put(k, v.Target, 8, payload) == nil {
					notify.Property = prop
				}
			case ok:
				mask := uint32(x.EventMaskPropertyChange | x.EventMaskStructureNotify)
				if x.ChangeWindowAttributesChecked(s.c, v.Requestor, x.CwEventMask, []uint32{mask}).Check() == nil {
					size := make([]byte, 4)
					xgb.Put32(size, uint32(min(uint64(len(payload)), uint64(^uint32(0)))))
					if s.put(k, s.atoms["INCR"], 32, size) == nil {
						transfers[k] = selectionTransfer{v.Target, payload}
						notify.Property = prop
					}
				}
			}
			// A requestor can exit at any time. Losing one consumer does not release the clipboard.
			if err := x.SendEventChecked(s.c, false, v.Requestor, 0, string(notify.Bytes())).Check(); err != nil {
				delete(transfers, k)
			}
		}
	}
}
