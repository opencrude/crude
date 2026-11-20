package anchor

import (
	"runtime"
	"strings"
	"testing"
)

const plan = "# Cache rename\n\n## Steps\n- Store sessions as JSON files.\n- Add a migration.\n- Store sessions as JSON files.\n"

func TestResolveFollowsTheQuoteAfterEditsBeforeIt(t *testing.T) {
	start := strings.Index(plan, "Add a migration")
	a := Capture(plan, start, start+len("Add a migration"))
	edited := "# Cache rename\n\nIntro paragraph the user wrote.\n\n## Steps\n- Store sessions as JSON files.\n- Add a migration.\n- Store sessions as JSON files.\n"
	got := Resolve(edited, a)
	if !got.Found || !got.Exact || edited[got.Start:got.End] != "Add a migration" {
		t.Fatalf("resolved %+v = %q", got, edited[got.Start:got.End])
	}
}

func TestResolveChoosesTheRepeatedQuoteByContext(t *testing.T) {
	second := strings.LastIndex(plan, "Store sessions")
	a := Capture(plan, second, second+len("Store sessions as JSON files."))
	// Text inserted at the top moves both occurrences; context still picks
	// the second one, not the occurrence nearest the old offset.
	edited := strings.Repeat("x", 40) + "\n" + plan
	got := Resolve(edited, a)
	if want := strings.LastIndex(edited, "Store sessions"); !got.Found || got.Start != want {
		t.Fatalf("resolved start %d, want %d (%+v)", got.Start, want, got)
	}
}

func TestResolveFindsASlightlyEditedQuote(t *testing.T) {
	start := strings.Index(plan, "Add a migration")
	a := Capture(plan, start, start+len("Add a migration."))
	edited := strings.Replace(plan, "Add a migration.", "Add one migration.", 1)
	got := Resolve(edited, a)
	if !got.Found || got.Exact {
		t.Fatalf("fuzzy result %+v", got)
	}
	if text := edited[got.Start:got.End]; text != "Add one migration." {
		t.Fatalf("fuzzy match %q", text)
	}
}

func TestResolveReportsRemovedTextNearItsOldPlace(t *testing.T) {
	start := strings.Index(plan, "Add a migration")
	a := Capture(plan, start, start+len("Add a migration."))
	edited := strings.Replace(plan, "- Add a migration.\n", "", 1)
	got := Resolve(edited, a)
	if got.Found {
		t.Fatalf("removed quote still found: %+v %q", got, edited[got.Start:got.End])
	}
	if got.Start > len(edited) || got.End > len(edited) || got.End < got.Start {
		t.Fatalf("hint out of range: %+v", got)
	}
}

func TestCaptureKeepsRuneBoundaries(t *testing.T) {
	text := "سلام دنیا plan"
	a := Capture(text, 1, 7) // inside the first and fourth runes
	if !strings.HasPrefix(text[a.Start:], a.Exact) || a.Start != 0 {
		t.Fatalf("capture %+v", a)
	}
	got := Resolve("پیش "+text, a)
	if !got.Found || !got.Exact {
		t.Fatalf("resolve %+v", got)
	}
}

func TestLines(t *testing.T) {
	start := strings.Index(plan, "- Store")
	end := strings.Index(plan, "- Add") + len("- Add a migration.\n")
	if first, last := Lines(plan, start, end); first != 4 || last != 5 {
		t.Fatalf("lines %d-%d, want 4-5", first, last)
	}
}

func TestFuzzyResolutionLateInALargeTextStaysBounded(t *testing.T) {
	line := "- A step of the plan that the user did not comment on.\n"
	text := strings.Repeat(line, (1<<20)/len(line)) + "- Ship the release candidate on Friday.\n"
	start := strings.LastIndex(text, "Ship")
	a := Capture(text, start, start+len("Ship the release candidate on Friday."))
	edited := strings.Replace(text, "Ship the release candidate on Friday.", "Ship the final release on Monday.", 1)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	got := Resolve(edited, a)
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("resolving one anchor allocated %d bytes", allocated)
	}
	if got.Found && !strings.Contains(edited[got.Start:got.End], "release") {
		t.Fatalf("resolved to %q", edited[got.Start:got.End])
	}
}

func TestResolveUnchangedUnicodeLateInText(t *testing.T) {
	text := strings.Repeat("مرحله بدون نظر.\n", 4000) + "مرحله آخر: انتشار.\n"
	start := strings.LastIndex(text, "انتشار")
	a := Capture(text, start, start+len("انتشار"))
	got := Resolve(text, a)
	if !got.Found || !got.Exact || got.Start != start {
		t.Fatalf("resolve %+v, want start %d", got, start)
	}
	edited := strings.Replace(text, "انتشار.", "انتشار نهایی.", 1)
	if got := Resolve(edited, a); !got.Found || edited[got.Start:got.End] != "انتشار" {
		t.Fatalf("edited resolve %+v", got)
	}
}
