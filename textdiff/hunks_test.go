package textdiff

import (
	"strings"
	"testing"
)

func TestContentHunksMatchCheckoutLineEndings(t *testing.T) {
	repository := "# Title\n\none\ntwo\nthree\n"
	checkout := strings.ReplaceAll(repository, "\n", "\r\n")
	if hunks := Hunks(repository, checkout); len(hunks) == 0 {
		t.Fatal("precondition: exact hunks see every converted line")
	}
	if hunks := ContentHunks(repository, checkout); len(hunks) != 0 {
		t.Fatalf("checkout conversion reported as changes: %#v", hunks)
	}
	edited := strings.Replace(checkout, "two", "TWO", 1)
	hunks := ContentHunks(repository, edited)
	if len(hunks) != 1 || hunks[0].OldStart != 3 || hunks[0].NewStart != 3 ||
		strings.Join(hunks[0].OldLines, "") != "two\n" || strings.Join(hunks[0].NewLines, "") != "TWO\r\n" {
		t.Fatalf("one-line edit = %#v, want exact bytes of line 4 only", hunks)
	}
	// Lone CR and a missing final newline remain real content differences.
	if hunks := ContentHunks("a\nb\n", "a\rb\n"); len(hunks) != 1 {
		t.Fatalf("lone CR hidden: %#v", hunks)
	}
	if hunks := ContentHunks("a\nb\n", "a\r\nb"); len(hunks) != 1 || hunks[0].OldStart != 1 {
		t.Fatalf("final newline change hidden: %#v", hunks)
	}
	if hunks := ContentHunks("", "a\r\n"); len(hunks) != 1 || len(hunks[0].NewLines) != 1 {
		t.Fatalf("added file = %#v", hunks)
	}
}

func TestReviewHunksKeepPureLineEndingChangesVisible(t *testing.T) {
	if hunks := ReviewHunks("a\nb\n", "a\r\nb\r\n"); len(hunks) != 1 || len(hunks[0].OldLines) != 2 {
		t.Fatalf("pure terminator change must stay reviewable: %#v", hunks)
	}
	if hunks := ReviewHunks("a\nb\n", "a\r\nB\r\n"); len(hunks) != 1 || hunks[0].OldStart != 1 {
		t.Fatalf("content change plus conversion = %#v", hunks)
	}
	if hunks := ReviewHunks("same\n", "same\n"); hunks != nil {
		t.Fatalf("identical = %#v", hunks)
	}
}
