package sessiondb

import "testing"

func TestCodeSelectionLabelsAndLines(t *testing.T) {
	ref := Reference{Kind: KindCodeSelection, Title: CodeSelectionTitle("[id].tsx", 4, 9)}
	if label := CodeSelectionLabel("[id].tsx", 4, 9); label != "[(id).tsx:4-9]" {
		t.Fatalf("label %q would end the chip at the file name's bracket", label)
	}
	if start, end, ok := ref.CodeLines(); !ok || start != 4 || end != 9 {
		t.Fatalf("lines = %d-%d %v", start, end, ok)
	}
	one := Reference{Kind: KindCodeSelection, Title: CodeSelectionTitle("a:b.go", 7, 7)}
	if one.Title != "a:b.go:7" {
		t.Fatalf("one-line title %q", one.Title)
	}
	if start, end, ok := one.CodeLines(); !ok || start != 7 || end != 7 {
		t.Fatalf("one line = %d-%d %v", start, end, ok)
	}
	for _, title := range []string{"x.go", "x.go:0", "x.go:9-3", "x.go:a-b"} {
		if _, _, ok := (Reference{Kind: KindCodeSelection, Title: title}).CodeLines(); ok {
			t.Fatalf("%q parsed as a line range", title)
		}
	}
	if _, _, ok := (Reference{Kind: KindSelectedFile, Title: "x.go:1"}).CodeLines(); ok {
		t.Fatal("a selected file is not a code selection")
	}
	text := Reference{ID: "r", Kind: KindCodeSelection, Label: "[s.go:2-3]", Title: "s.go:2-3", FilePath: "/w/s.go", Content: "a\nb"}.AgentText()
	if text != "Reference [s.go:2-3] (r): lines 2-3 of /w/s.go, as the editor showed them when the user asked:\na\nb" {
		t.Fatalf("agent text %q", text)
	}
}
