package coreclient

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"crude/terminalvt"
	"crude/workspace"
)

// FuzzTerminalInputAdmissionOrdering exercises the public admission boundary and
// real batch drain. The oracle is the accepted ordered events, not a second
// implementation of the batching algorithm. Motions may disappear; buttons,
// keys, paste, focus and resize must appear exactly once and in admission order.
func FuzzTerminalInputAdmissionOrdering(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 255})
	f.Add(make([]byte, 600))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			data = data[:4096]
		}
		s := &TerminalInputStream{wake: make(chan struct{}, 1)}
		var want, got []workspace.TerminalInput
		now := time.Unix(0, 0)
		drain := func() {
			now = now.Add(20 * time.Millisecond)
			batch, _ := s.takeBatch(nil, now)
			for _, item := range batch {
				if item.Kind != "mouse" || item.Mouse.Action != terminalvt.MouseMotion {
					got = append(got, item)
				}
			}
		}
		for i, b := range data {
			item := workspace.TerminalInput{}
			switch b % 8 {
			case 0:
				item.Kind, item.Mouse = "mouse", terminalvt.Mouse{Action: terminalvt.MouseMotion, X: i}
			case 1:
				item.Kind, item.Key = "key", terminalvt.Key{Code: rune('a' + b%26), Text: string(rune('a' + b%26))}
			case 2:
				item.Kind, item.Data = "paste", []byte{b, byte(i)}
			case 3:
				item.Kind, item.Mouse = "mouse", terminalvt.Mouse{Action: terminalvt.MousePress, Button: terminalvt.MouseLeft, X: i}
			case 4:
				item.Kind, item.Mouse = "mouse", terminalvt.Mouse{Action: terminalvt.MouseRelease, Button: terminalvt.MouseLeft, X: i}
			case 5:
				item.Kind, item.Focused = "focus", b&8 != 0
			case 6:
				item.Kind, item.Cols, item.Rows = "resize", 8+int(b), 2+i%50
			case 7:
				drain()
				continue
			}
			err := s.Submit(item)
			if err == nil && (item.Kind != "mouse" || item.Mouse.Action != terminalvt.MouseMotion) {
				owned := item
				owned.Data = append([]byte(nil), item.Data...)
				want = append(want, owned)
			} else if err != nil && !errors.Is(err, workspace.ErrTerminalBackpressure) {
				t.Fatal(err)
			}
			// The caller may reuse the paste buffer immediately after admission.
			clear(item.Data)
		}
		for len(s.queue) > 0 || s.motion != nil {
			drain()
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("accepted input lost/reordered/replayed: got=%v want=%v", got, want)
		}
		if s.bytes != 0 {
			t.Fatalf("retained byte credit after drain: %d", s.bytes)
		}
		if len(data) > 0 {
			for i := 0; i < 300; i++ {
				if err := s.Submit(workspace.TerminalInput{Kind: "mouse", Mouse: terminalvt.Mouse{Action: terminalvt.MouseMotion, X: i}}); err != nil {
					t.Fatal(err)
				}
			}
			now = now.Add(20 * time.Millisecond)
			batch, _ := s.takeBatch(nil, now)
			if len(batch) != 1 || batch[0].Mouse.X != 299 {
				t.Fatal("final pointer position lost", batch)
			}
		}
	})
}
