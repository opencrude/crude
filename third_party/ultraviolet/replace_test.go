package uv

import "testing"

func TestReplaceTracksChangedRowsAndErasure(t *testing.T) {
	b := NewRenderBuffer(8, 3)
	src := NewBuffer(8, 3)
	b.Replace(src)
	if b.TouchedLines() != 0 {
		t.Fatal("unchanged snapshot invalidated rows")
	}
	src.SetCell(2, 1, &Cell{Content: "界", Width: 2})
	b.Replace(src)
	if b.TouchedLines() != 1 || b.Touched[1].FirstCell != 2 || b.Touched[1].LastCell != 4 || !b.CellAt(3, 1).IsZero() {
		t.Fatalf("wide cell damage: %#v / %#v", b.Touched, b.Lines)
	}
	b.Touched = make([]*LineData, 3)
	src.Clear()
	b.Replace(src)
	if b.TouchedLines() != 1 || b.Touched[1].FirstCell != 2 || b.Touched[1].LastCell != 4 {
		t.Fatalf("erasure damage: %#v", b.Touched)
	}
	if !b.CellAt(2, 1).Equal(&EmptyCell) || !b.CellAt(3, 1).Equal(&EmptyCell) {
		t.Fatal("wide cell survived complete erasure")
	}
	for i := range b.Touched {
		b.Touched[i] = &LineData{FirstCell: -1, LastCell: -1}
	}
	b.Replace(src)
	if b.TouchedLines() != 0 {
		t.Fatal("renderer clean-row sentinel counted as damage")
	}
}
