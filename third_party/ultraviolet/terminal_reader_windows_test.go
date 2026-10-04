//go:build windows

package uv

import (
	"bytes"
	"encoding/binary"
	"testing"

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

func win32KeyInputRecord(virtualKey uint16, char rune, mods uint32) xwindows.InputRecord {
	record := xwindows.InputRecord{EventType: xwindows.KEY_EVENT}
	binary.LittleEndian.PutUint32(record.Event[0:4], 1)
	binary.LittleEndian.PutUint16(record.Event[4:6], 1)
	binary.LittleEndian.PutUint16(record.Event[6:8], virtualKey)
	binary.LittleEndian.PutUint16(record.Event[10:12], uint16(char))
	binary.LittleEndian.PutUint32(record.Event[12:16], mods)
	return record
}
