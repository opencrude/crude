package input

import (
	"os"
	"path/filepath"
	"testing"

	"crude/sessiondb"
	"crude/tui/theme"
	"crude/tui/ui"
)

// TestApplyReferenceRangesErrorTone guards the chip display-state contract:
// file-backed references whose backing file vanished render in the error
// tone, while healthy inline references stay normal. A stale attachment must
// be visible BEFORE submit, not silently sent as a dead reference.
func TestApplyReferenceRangesErrorTone(t *testing.T) {
	model := NewModel(theme.DefaultDark())
	store := &pasteReferenceStore{}

	gonePath := filepath.Join(t.TempDir(), "vanished.png")
	store.references = []sessiondb.Reference{
		{ID: "r-file", Kind: sessiondb.KindSelectedFile, Label: "[File1]", FilePath: gonePath},
		{ID: "r-text", Kind: sessiondb.KindPastedText, Label: "[Pasted Text1]", Content: "hello", Inline: true},
	}
	model.SetReferenceStore(store)
	model.SetInput("see [File1] and [Pasted Text1]")

	ranges := model.input.AtomicRanges()
	if len(ranges) != 2 {
		t.Fatalf("ranges = %+v, want 2", ranges)
	}
	tones := map[string]ui.ChipTone{}
	for _, ar := range ranges {
		tones[ar.ID] = ar.Tone
	}
	if tones["r-file"] != ui.ChipToneError {
		t.Fatalf("missing-file chip tone = %d, want ChipToneError", tones["r-file"])
	}
	if tones["r-text"] != ui.ChipToneNormal {
		t.Fatalf("healthy chip tone = %d, want ChipToneNormal", tones["r-text"])
	}

	// Re-adding the file heals the chip back to the normal tone.
	if err := os.WriteFile(gonePath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	model.SetInput("see [File1] and [Pasted Text1]")
	for _, ar := range model.input.AtomicRanges() {
		if ar.ID == "r-file" && ar.Tone != ui.ChipToneNormal {
			t.Fatalf("restored file still error tone: %d", ar.Tone)
		}
	}
}
