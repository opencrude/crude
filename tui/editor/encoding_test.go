package editor

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"crude/textencoding"
	"crude/tui/theme"
)

func TestStandaloneEditorDecodesWithoutLosingDiskIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "encoded.txt")
	raw := []byte{0xfe, 0xff, 0, 'A', 0, 13, 0, 10}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	defer ed.Close()
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	s := ed.BufferSnapshot()
	if ed.Value() != "A\r\n" || ed.Modified() || s.Encoding != textencoding.UTF16BE || s.ExpectedHash != sha256.Sum256(raw) {
		t.Fatalf("standalone byte/text boundary = %+v, %q", s, ed.Value())
	}
	buffer, err := NewBufferManager().OpenFile(path)
	if err != nil || string(buffer.Bytes()) != "A\r\n" || buffer.Snapshot().Encoding != s.Encoding {
		t.Fatal("managed standalone buffer disagrees with editor:", err)
	}
}
