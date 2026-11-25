//go:build windows

package uv

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"

	xwindows "github.com/charmbracelet/x/windows"
)

func TestVTInputPreservesWindowsControlChord(t *testing.T) {
	for _, test := range []struct {
		name string
		char rune
		mods uint32
		want KeyMod
	}{
		{"zero char ctrl v", 0, xwindows.LEFT_CTRL_PRESSED, ModCtrl},
		{"translated ctrl shift v", 0x16, xwindows.LEFT_CTRL_PRESSED | xwindows.SHIFT_PRESSED, ModCtrl | ModShift},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := NewTerminalReader(bytes.NewReader(nil), "dumb")
			reader.vtInput = true
			var encoded bytes.Buffer
			reader.serializeWin32InputRecords([]xwindows.InputRecord{
				win32KeyInputRecord('V', test.char, test.mods),
			}, &encoded)

			n, event := reader.Decode(encoded.Bytes())
			if n != encoded.Len() {
				t.Fatalf("decoded %d/%d bytes", n, encoded.Len())
			}
			key, ok := event.(KeyPressEvent)
			if !ok {
				t.Fatalf("event = %T, want KeyPressEvent", event)
			}
			if key.Code != 'v' || key.Mod != test.want || key.Text != "" {
				t.Fatalf("key = %+v, want code=v mod=%v without text", key, test.want)
			}
		})
	}
}

func TestVTInputConsoleText(t *testing.T) {
	for _, text := range []string{"😂", "a😂😂b", "☀️e\u0301👩‍💻!"} {
		for _, paste := range []bool{false, true} {
			reader := NewTerminalReader(bytes.NewReader(nil), "")
			reader.vtInput = true
			var encoded bytes.Buffer
			send := func(text string) {
				for _, unit := range utf16.Encode([]rune(text)) {
					press := win32KeyInputRecord(0, rune(unit), 0)
					release := press
					binary.LittleEndian.PutUint32(release.Event[:4], 0)
					// Console reads need not contain a complete pair.
					reader.serializeWin32InputRecords([]xwindows.InputRecord{press, release}, &encoded)
				}
			}
			if paste {
				send("\x1b[200~")
			}
			send(text)
			if paste {
				send("\x1b[201~")
			}
			_, events := reader.eventScanner.scanEvents(encoded.Bytes(), false)
			var typed, pasted strings.Builder
			for _, event := range events {
				switch e := event.(type) {
				case KeyPressEvent:
					typed.WriteString(e.Text)
				case PasteEvent:
					pasted.WriteString(e.Content)
				}
			}
			if (paste && (pasted.String() != text || typed.Len() != 0)) ||
				(!paste && (typed.String() != text || pasted.Len() != 0)) {
				t.Errorf("paste=%v, sent %q: typed %q, pasted %q", paste, text, typed.String(), pasted.String())
			}
		}
	}
}

// A native ConPTY can commit each unmappable UTF-16 unit on Alt release.
// Its preceding keydown has no character and must not interrupt a pair.
func TestVTInputAltReleaseText(t *testing.T) {
	const text = "e\u0301☀️👩‍💻😂😂"
	reader := NewTerminalReader(bytes.NewReader(nil), "")
	reader.vtInput = true
	var encoded bytes.Buffer
	for _, unit := range utf16.Encode([]rune(text)) {
		press := win32KeyInputRecord(xwindows.VK_MENU, 0, xwindows.LEFT_ALT_PRESSED)
		release := win32KeyInputRecord(xwindows.VK_MENU, rune(unit), 0)
		binary.LittleEndian.PutUint32(release.Event[:4], 0)
		reader.serializeWin32InputRecords([]xwindows.InputRecord{press, release}, &encoded)
	}
	if got := encoded.String(); got != text {
		t.Fatalf("Alt-release stream %q; want %q", got, text)
	}
}

func TestVTInputTruncatedPairPreservesNextKey(t *testing.T) {
	reader := NewTerminalReader(bytes.NewReader(nil), "")
	reader.vtInput = true
	var encoded bytes.Buffer
	repeat := win32KeyInputRecord('A', 'a', 0)
	binary.LittleEndian.PutUint16(repeat.Event[4:6], 2)
	reader.serializeWin32InputRecords([]xwindows.InputRecord{
		win32KeyInputRecord(0, 0xd83d, 0), repeat,
	}, &encoded)
	if got := encoded.String(); got != "\uFFFDaa" {
		t.Fatalf("truncated pair followed by repeated key = %q", got)
	}
}

func win32KeyInputRecord(virtualKey uint16, char rune, mods uint32) xwindows.InputRecord {
	record := xwindows.InputRecord{EventType: xwindows.KEY_EVENT}
	binary.LittleEndian.PutUint32(record.Event[0:4], 1)
	binary.LittleEndian.PutUint16(record.Event[4:6], 1)
	binary.LittleEndian.PutUint16(record.Event[6:8], virtualKey)
	binary.LittleEndian.PutUint16(record.Event[10:12], uint16(char))
	binary.LittleEndian.PutUint32(record.Event[12:16], mods)
	return record
}
