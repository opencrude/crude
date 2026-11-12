package editor

import (
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/types"
)

// The cases and expected ranges are VS Code's own suites "Indentation
// Folding" and "Folding with regions" (microsoft/vscode, src/vs/editor/
// contrib/folding/test/browser/indentRangeProvider.test.ts at
// e608ca420ca76f4bfc2cfbff02538d66242e8174, MIT). Expected lines are VS
// Code's one-based numbers; the port uses zero-based lines. The flags case is
// omitted: catalog markers carry no regular-expression flags.
func TestIndentFoldsMatchVSCodeSuite(t *testing.T) {
	markers := regexp.MustCompile(`(^\s*#region\b)|(?:^\s*#endregion\b)`)
	cases := []struct {
		name    string
		lines   []string
		offSide bool
		markers bool
		want    [][2]int
	}{
		{"one level", []string{"A", "  A", "  A", "  A"}, false, false, [][2]int{{1, 4}}},
		{"one level offside", []string{"A", "  A", "  A", "  A"}, true, false, [][2]int{{1, 4}}},
		{"two levels", []string{"A", "  A", "  A", "    A", "    A"}, false, false, [][2]int{{1, 5}, {3, 5}}},
		{"three levels", []string{"A", "  A", "    A", "      A", "A"}, true, false, [][2]int{{1, 4}, {2, 4}, {3, 4}}},
		{"decreasing indent", []string{"    A", "  A", "A"}, false, false, nil},
		{"java", []string{"class A {", "  void foo() {", "    console.log();", "    console.log();", "  }", "", "  void bar() {", "    console.log();", "  }", "}", "interface B {", "  void bar();", "}"}, false, false, [][2]int{{1, 9}, {2, 4}, {7, 8}, {11, 12}}},
		{"javadoc", []string{"/**", " * Comment", " */", "class A {", "  void foo() {", "  }", "}"}, false, false, [][2]int{{1, 3}, {4, 6}}},
		{"whitespace java", []string{"class A {", "", "  void foo() {", "     ", "     return 0;", "  }", "      ", "}"}, false, false, [][2]int{{1, 7}, {3, 5}}},
		{"whitespace python", []string{"def a:", "  pass", "   ", "  def b:", "    pass", "  ", "      ", "def c: # since there was a deintent here"}, true, false, [][2]int{{1, 5}, {4, 5}}},
		{"tabs", []string{"class A {", "\t\t", "\tvoid foo() {", "\t \t//hello", "\t    return 0;", "  \t}", "      ", "}"}, false, false, [][2]int{{1, 7}, {3, 5}}},
		{"inside region, indented", []string{"class A {", "  #region", "  void foo() {", "     ", "     return 0;", "  }", "  #endregion", "}"}, false, true, [][2]int{{1, 7}, {2, 7}, {3, 5}}},
		{"inside region, not indented", []string{"var x;", "#region", "void foo() {", "     ", "     return 0;", "  }", "#endregion", ""}, false, true, [][2]int{{2, 7}, {3, 6}}},
		{"empty regions", []string{"var x;", "#region", "#endregion", "#region", "", "#endregion", "var y;"}, false, true, [][2]int{{2, 3}, {4, 6}}},
		{"nested regions", []string{"var x;", "#region", "#region", "", "#endregion", "#endregion", "var y;"}, false, true, [][2]int{{2, 6}, {3, 5}}},
		{"nested regions 2", []string{"class A {", "  #region", "", "  #region", "", "  #endregion", "  // comment", "  #endregion", "}"}, false, true, [][2]int{{1, 8}, {2, 8}, {4, 6}}},
		{"incomplete regions", []string{"class A {", "#region", "  // comment", "}"}, false, true, [][2]int{{2, 3}}},
		{"incomplete regions 2", []string{"", "#region", "#region", "#region", "  // comment", "#endregion", "#endregion", " // hello"}, false, true, [][2]int{{3, 7}, {4, 6}}},
		{"indented region before", []string{"if (x)", "  return;", "", "#region", "  // comment", "#endregion"}, false, true, [][2]int{{1, 3}, {4, 6}}},
		{"indented region before 2", []string{"if (x)", "  log();", "", "    #region", "      // comment", "    #endregion"}, false, true, [][2]int{{1, 6}, {2, 6}, {4, 6}}},
		{"indented region in-between", []string{"#region", "  // comment", "  if (x)", "    return;", "", "#endregion"}, false, true, [][2]int{{1, 6}, {3, 5}}},
		{"indented region after", []string{"#region", "  // comment", "", "#endregion", "  if (x)", "    return;"}, false, true, [][2]int{{1, 4}, {5, 6}}},
		{"with off-side", []string{"#region", "  ", "", "#endregion", ""}, true, true, [][2]int{{1, 4}}},
		{"nested with off-side", []string{"#region", "  ", "#region", "", "#endregion", "", "#endregion", ""}, true, true, [][2]int{{1, 7}, {3, 5}}},
		{"issue 35981", []string{"function thisFoldsToEndOfPage() {", "  const variable = []", "    // #region", "    .reduce((a, b) => a,[]);", "}", "", "function thisFoldsProperly() {", "  const foo = \"bar\"", "}"}, false, true, [][2]int{{1, 4}, {2, 4}, {7, 8}}},
		{"misspelled markers", []string{"#Region", "#endregion", "#regionsandmore", "#endregion", "#region", "#end region", "#region", "#endregionff"}, true, true, nil},
		{"issue 79359", []string{"#region", "", "class A", "  foo", "", "class A", "  foo", "", "#endregion"}, true, true, [][2]int{{1, 9}, {3, 4}, {6, 7}}},
	}
	for _, c := range cases {
		var pattern *regexp.Regexp
		if c.markers {
			pattern = markers
		}
		got := scanLines(c.lines, c.offSide, pattern, maxIndentFolds)
		var want []FoldRange
		for _, r := range c.want {
			want = append(want, FoldRange{r[0] - 1, r[1] - 1})
		}
		if len(got) != 0 || len(want) != 0 {
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s: got %v, want %v", c.name, got, want)
			}
		}
	}
}

func scanLines(lines []string, offSide bool, markers *regexp.Regexp, limit int) []FoldRange {
	var scan indentFoldScan
	facts := func(i int) (int, int) { return foldLineFacts([]byte(lines[i]), 4, markers) }
	return slices.Clone(scan.compute(len(lines), facts, offSide, limit))
}

// Over the region limit VS Code keeps every range of the smallest indents
// and fills the rest in document order, so outer blocks stay foldable.
func TestIndentFoldsLimitKeepsOuterBlocks(t *testing.T) {
	lines := strings.Split(strings.TrimSuffix(strings.Repeat("A\n  B\n    c\n", 3), "\n"), "\n")
	got := scanLines(lines, false, nil, 4)
	want := []FoldRange{{0, 2}, {1, 2}, {3, 5}, {6, 8}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("limited folds %v, want %v", got, want)
	}
}

// The editor takes region markers and offSide from the catalog for the
// document's language: a Go "// region" comment pair folds, and a Python
// block folds without its trailing blank lines.
func TestEditorFoldsUseCatalogMarkersAndOffSide(t *testing.T) {
	goSource := "package p\n\n// region helpers\nfunc a() {}\n\nfunc b() {}\n// endregion\n"
	e := loadEditorText(t, "regions.go", goSource)
	paintEditor(e, 80, 14)
	if r, ok := e.foldAt(2); !ok || r != (FoldRange{2, 6}) {
		t.Fatalf("region marker fold %v %v, ranges %v", r, ok, e.folds.ranges)
	}
	e.SetCursorPosition(2, 0)
	e.HandleEvent(tea.KeyPressMsg{Code: '-', Mod: tea.ModAlt})
	if buf := paintEditor(e, 80, 14); e.ScrollModel().Content() != 4 || !strings.Contains(plainRow(buf, 2, 80), "region helpers") {
		t.Fatalf("folded region shows %d rows, header %q", e.ScrollModel().Content(), plainRow(buf, 2, 80))
	}

	py := loadEditorText(t, "blocks.py", "def a():\n    pass\n\n\ndef b():\n    pass\n")
	paintEditor(py, 80, 14)
	if r, ok := py.foldAt(0); !ok || r != (FoldRange{0, 1}) {
		t.Fatalf("off-side block fold %v %v, ranges %v", r, ok, py.folds.ranges)
	}
	c := loadEditorText(t, "blocks.c", "int a() {\n    return 0;\n\n\n}\n")
	paintEditor(c, 80, 14)
	if r, ok := c.foldAt(0); !ok || r != (FoldRange{0, 3}) {
		t.Fatalf("brace block fold %v %v, ranges %v", r, ok, c.folds.ranges)
	}
}

// Edits update the per-line cache through the line-change journal. After
// every kind of edit the cached ranges equal a fresh scan of the whole text.
func TestIndentFoldCacheFollowsEdits(t *testing.T) {
	src := "package p\n\n// region a\nfunc a() {\n\tif x {\n\t\tcall()\n\t}\n}\n// endregion\n\nfunc b() {\n\tcall()\n}\n"
	e := loadEditorText(t, "cache.go", src)
	check := func(step string) {
		t.Helper()
		paintEditor(e, 80, 30)
		lines := strings.Split(e.Value(), "\n")
		var scan indentFoldScan
		markers := e.languageFacts().markers
		want := normalizeFolds(slices.Clone(scan.compute(len(lines), func(i int) (int, int) {
			return foldLineFacts([]byte(lines[i]), e.TabWidth(), markers)
		}, false, maxIndentFolds)))
		if !reflect.DeepEqual(e.folds.ranges, want) {
			t.Fatalf("%s: cached folds %v, fresh scan %v\n%s", step, e.folds.ranges, want, e.Value())
		}
	}
	check("load")
	key := func(code rune, text string) { e.HandleEvent(tea.KeyPressMsg{Code: code, Text: text}) }
	e.SetCursorPosition(5, 8)
	key(tea.KeyEnter, "")
	check("split a body line")
	key('y', "y")
	check("type on the new line")
	e.SetCursorPosition(11, 0)
	e.HandleEvent(tea.PasteMsg{Content: "\tif y {\n\t\tz()\n\t}\n"})
	check("paste a nested block")
	e.SetSelection(3, 0, 8, 1)
	key(tea.KeyBackspace, "")
	check("delete a block")
	e.SetCursorPosition(2, 0)
	key(tea.KeyTab, "")
	check("indent a marker")
	e.Undo()
	e.Undo()
	check("undo")
	e.SetPresentation(types.EditorSettings{TabSize: 2, LineNumbers: true})
	check("tab width")
}
