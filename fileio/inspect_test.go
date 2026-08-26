package fileio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHexDumpFormat guards the dump shape: 8-digit offset, space-separated
// hex pairs with a mid-row gap, and a printable ASCII gutter.
func TestHexDumpFormat(t *testing.T) {
	data := make([]byte, 20)
	copy(data, "PK\x03\x04\x00\x00binary!")
	got := HexDump(data, 512)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 rows for 20 bytes, got %d:\n%s", len(lines), got)
	}
	if !strings.HasPrefix(lines[0], "00000000 ") {
		t.Fatalf("missing zero offset: %q", lines[0])
	}
	if !strings.Contains(lines[0], "50 4b 03 04") {
		t.Fatalf("hex pairs missing: %q", lines[0])
	}
	if !strings.Contains(lines[0], "|PK....binary!") {
		t.Fatalf("ascii gutter missing or wrong tail: %q", lines[0])
	}

	// A byte cap renders exactly one row.
	if capped := HexDump(data, 8); strings.Contains(strings.TrimRight(capped, "\n"), "\n") {
		t.Fatalf("capped dump must render a single row: %q", capped)
	}
}

// TestInspectFileClassifiesAndBounds guards the shared attachment inspector:
// text files decode inline; binaries get a hex dump instead of raw garbage;
// oversized files truncate with an honest marker.
func TestInspectFileClassifiesAndBounds(t *testing.T) {
	dir := t.TempDir()

	textPath := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(textPath, []byte("hello attachment\nline two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "blob.bin")
	binData := append([]byte{0x89, 'P', 'N', 'G', 0x00, 0x01, 0xff, 0xfe}, make([]byte, 40)...)
	if err := os.WriteFile(binPath, binData, 0o600); err != nil {
		t.Fatal(err)
	}

	textPrev, err := InspectFile(textPath, DefaultPreviewBytes, DefaultHexHeadBytes)
	if err != nil {
		t.Fatal(err)
	}
	if textPrev.Binary {
		t.Fatalf("text classified binary: %+v", textPrev)
	}
	if textPrev.Text != "hello attachment\nline two\n" {
		t.Fatalf("text preview = %q", textPrev.Text)
	}
	if got := textPrev.ReadableText(); got != textPrev.Text {
		t.Fatalf("readable text should pass content through: %q", got)
	}

	binPrev, err := InspectFile(binPath, DefaultPreviewBytes, DefaultHexHeadBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !binPrev.Binary {
		t.Fatalf("binary misclassified as text: %+v", binPrev)
	}
	if !strings.Contains(binPrev.HexDump, "00000000 ") || !strings.Contains(binPrev.HexDump, "89") {
		t.Fatalf("hex dump incomplete: %q", binPrev.HexDump)
	}
	rendered := binPrev.ReadableText()
	for _, want := range []string{"Binary file (", "Hex dump of first", "execute_command"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("binary summary missing %q:\n%s", want, rendered)
		}
	}
	if strings.ContainsRune(rendered, 0x00) {
		t.Fatal("rendered summary leaked a raw NUL byte")
	}

	// Truncation: cap below the text size marks the flag and appends a note.
	smallPrev, err := InspectFile(textPath, 5, DefaultHexHeadBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !smallPrev.Truncated {
		t.Fatalf("small-cap inspect must report truncation: %+v", smallPrev)
	}
	if rendered := smallPrev.ReadableText(); !strings.Contains(rendered, "[truncated") {
		t.Fatalf("truncation note missing: %q", rendered)
	}
}
