package rendering

import "testing"

func TestTextBoundariesKeepCompleteSourceClusters(t *testing.T) {
	runes := []rune("Ae\u0301👩🏽‍💻🇺🇳Z")
	boundaries := []int{0, 1, 3, 7, 9, 10}
	for i, pos := range boundaries {
		if got := GraphemeBoundary(runes, pos); got != pos {
			t.Fatalf("boundary %d -> %d", pos, got)
		}
		if i > 0 && PreviousGrapheme(runes, pos) != boundaries[i-1] {
			t.Fatalf("previous from %d", pos)
		}
		if i+1 < len(boundaries) && NextGrapheme(runes, pos) != boundaries[i+1] {
			t.Fatalf("next from %d", pos)
		}
	}
	for _, step := range []struct{ pos, want int }{{2, 1}, {4, 3}, {5, 3}, {6, 3}, {8, 7}} {
		if got := GraphemeBoundary(runes, step.pos); got != step.want {
			t.Fatalf("inside %d -> %d want %d", step.pos, got, step.want)
		}
	}
}

func TestTextBoundariesRespectUnicodePrependBeforeASCII(t *testing.T) {
	runes := []rune("\u0600AB")
	if got := PreviousGrapheme(runes, 2); got != 0 {
		t.Fatalf("previous split prepend cluster at %d", got)
	}
	if got := GraphemeBoundary(runes, 1); got != 0 {
		t.Fatalf("snap split prepend cluster at %d", got)
	}
}
