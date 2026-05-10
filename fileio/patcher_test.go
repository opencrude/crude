package fileio

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================================
// Basic matching
// ============================================================================

func TestApplySearchReplace_basicExactMatch(t *testing.T) {
	content := "line1\nline2\nline3"
	ops := []SearchReplace{{Search: "line2", Replace: "replaced"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "line1\nreplaced\nline3", got)
}

func TestApplySearchReplace_multiLineExact(t *testing.T) {
	content := "a\nb\nc\nd\ne"
	ops := []SearchReplace{{Search: "b\nc\nd", Replace: "x\ny"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nx\ny\ne", got)
}

func TestApplySearchReplace_searchEqualsReplace(t *testing.T) {
	content := "hello\nworld"
	ops := []SearchReplace{{Search: "world", Replace: "world"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, content, got)
}

func TestApplySearchReplace_emptySearch(t *testing.T) {
	_, err := ApplySearchReplace("hello", []SearchReplace{{Search: "", Replace: "x"}})
	if err == nil {
		t.Fatal("expected error for empty search")
	}
}

func TestApplySearchReplace_searchNotFound(t *testing.T) {
	_, err := ApplySearchReplace("hello\nworld", []SearchReplace{{Search: "missing", Replace: "x"}})
	if err == nil {
		t.Fatal("expected error for not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApplySearchReplace_multipleExactMatches(t *testing.T) {
	content := "foo\nbar\nfoo\nbar"
	_, err := ApplySearchReplace(content, []SearchReplace{{Search: "foo\nbar", Replace: "x"}})
	if err == nil {
		t.Fatal("expected error for multiple matches")
	}
	if !strings.Contains(err.Error(), "matches 2 locations") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApplySearchReplace_multipleWSMatchesButUniqueExact(t *testing.T) {
	content := "  foo\n  bar\nfoo\nbar"
	ops := []SearchReplace{{Search: "foo\nbar", Replace: "replaced"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "  foo\n  bar\nreplaced", got)
}

func TestApplySearchReplace_multipleWSMatchesError(t *testing.T) {
	content := "  a\n  b\na\n  b"
	_, err := ApplySearchReplace(content, []SearchReplace{{Search: "a\nb", Replace: "x"}})
	if err == nil {
		t.Fatal("expected error for multiple WS matches")
	}
}

// ============================================================================
// Leading/trailing blank lines in search
// ============================================================================

func TestApplySearchReplace_leadingNewlineInSearch(t *testing.T) {
	content := "header\n\ndef do_thing():\n    pass"
	search := "\ndef do_thing():\n    pass"
	ops := []SearchReplace{{Search: search, Replace: "def new_func():\n    pass"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "header\n\ndef new_func():\n    pass", got)
}

func TestApplySearchReplace_trailingNewlineInSearch(t *testing.T) {
	content := "line1\nline2\nline3"
	search := "line2\n"
	ops := []SearchReplace{{Search: search, Replace: "replaced"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "line1\nreplaced\nline3", got)
}

func TestApplySearchReplace_leadingAndTrailingBlankSearch(t *testing.T) {
	content := "a\n\n\nb\n\nc"
	search := "\n\nb\n\n"
	ops := []SearchReplace{{Search: search, Replace: "B"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\n\n\nB\n\nc", got)
}

func TestApplySearchReplace_searchIsOnlyBlankLines(t *testing.T) {
	content := "a\n\n\nb"
	search := " \n\n\t"
	_, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: "x"}})
	if err == nil {
		t.Fatal("expected error for all-whitespace search")
	}
}

// ============================================================================
// Replace size variations
// ============================================================================

func TestApplySearchReplace_replaceSmaller(t *testing.T) {
	content := "a\nb\nc\nd\ne"
	ops := []SearchReplace{{Search: "b\nc\nd", Replace: "x"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nx\ne", got)
}

func TestApplySearchReplace_replaceBigger(t *testing.T) {
	content := "a\nb\ne"
	ops := []SearchReplace{{Search: "b", Replace: "c\nd\ne\nf"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nc\nd\ne\nf\ne", got)
}

func TestApplySearchReplace_replaceToEmpty(t *testing.T) {
	content := "a\nb\nc"
	ops := []SearchReplace{{Search: "b", Replace: ""}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\n\nc", got)
}

func TestApplySearchReplace_replaceToEmpty_multiline(t *testing.T) {
	content := "a\nb\nc\nd"
	ops := []SearchReplace{{Search: "b\nc", Replace: ""}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\n\nd", got)
}

// ============================================================================
// Indentation
// ============================================================================

func TestApplySearchReplace_sameIndentation_exactMatch(t *testing.T) {
	content := "def foo():\n    x = 1\n    y = 2\n    return x"
	ops := []SearchReplace{{Search: "    x = 1\n    y = 2", Replace: "    a = 10\n    b = 20"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "def foo():\n    a = 10\n    b = 20\n    return x", got)
}

func TestApplySearchReplace_differentIndentation_wsMatch(t *testing.T) {
	content := "def foo():\n    x = 1\n    y = 2"
	search := "x = 1\ny = 2"
	ops := []SearchReplace{{Search: search, Replace: "a = 10\nb = 20"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "def foo():\n    a = 10\n    b = 20", got)
}

func TestApplySearchReplace_tabIndentation(t *testing.T) {
	content := "def foo():\n\tx = 1\n\ty = 2"
	search := "x = 1\ny = 2"
	ops := []SearchReplace{{Search: search, Replace: "a = 10"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "def foo():\n\ta = 10", got)
}

func TestApplySearchReplace_mixedIndentation_tabToSpace(t *testing.T) {
	content := "def foo():\n    x = 1\n    y = 2"
	search := "\tx = 1\n\ty = 2"
	ops := []SearchReplace{{Search: search, Replace: "a = 1"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "def foo():\n    a = 1", got)
}

func TestApplySearchReplace_mixedIndentation_spaceToTab(t *testing.T) {
	content := "def foo():\n\tx = 1\n\ty = 2"
	search := "    x = 1\n    y = 2"
	ops := []SearchReplace{{Search: search, Replace: "a = 1"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "def foo():\n\ta = 1", got)
}

func TestApplySearchReplace_reindentDeeperLevel(t *testing.T) {
	content := "def outer():\n        x = 1\n        y = 2"
	search := "  x = 1\n  y = 2"
	ops := []SearchReplace{{Search: search, Replace: "  a = 10\n  b = 20"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "def outer():\n        a = 10\n        b = 20", got)
}

func TestApplySearchReplace_reindentShallowerLevel(t *testing.T) {
	content := "if True:\n  x = 1\n  y = 2"
	search := "        x = 1\n        y = 2"
	ops := []SearchReplace{{Search: search, Replace: "        a = 10\n        b = 20"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "if True:\n  a = 10\n  b = 20", got)
}

// ============================================================================
// CRLF handling
// ============================================================================

func TestApplySearchReplace_crlfContent(t *testing.T) {
	content := "line1\r\nline2\r\nline3"
	ops := []SearchReplace{{Search: "line2", Replace: "replaced"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "line1\r\nreplaced\r\nline3", got)
}

func TestApplySearchReplace_crlfInSearchReplace(t *testing.T) {
	content := "a\nb\nc"
	search := "a\r\nb"
	replace := "x\r\ny"
	ops := []SearchReplace{{Search: search, Replace: replace}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "x\ny\nc", got)
}

func TestApplySearchReplace_mixedCrlfLf(t *testing.T) {
	content := "a\r\nb\nc\r\n"
	ops := []SearchReplace{{Search: "b", Replace: "X"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\r\nX\r\nc\r\n", got)
}

func TestApplySearchReplace_lfContent_staysLf(t *testing.T) {
	content := "a\nb\nc"
	ops := []SearchReplace{{Search: "b", Replace: "X"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nX\nc", got)
}

// ============================================================================
// Sequential operations
// ============================================================================

func TestApplySearchReplace_sequentialOps(t *testing.T) {
	content := "a\nb\nc\nd\ne"
	ops := []SearchReplace{
		{Search: "b\nc", Replace: "X"},
		{Search: "d", Replace: "Y"},
	}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nX\nY\ne", got)
}

func TestApplySearchReplace_sequentialFirstFails(t *testing.T) {
	_, err := ApplySearchReplace("a\nb", []SearchReplace{
		{Search: "missing", Replace: "X"},
		{Search: "a", Replace: "Y"},
	})
	if err == nil {
		t.Fatal("expected error from first op")
	}
}

func TestApplySearchReplace_sequentialWithNoops(t *testing.T) {
	content := "a\nb"
	ops := []SearchReplace{
		{Search: "a", Replace: "a"},
		{Search: "b", Replace: "B"},
	}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nB", got)
}

// ============================================================================
// Single-line edge cases
// ============================================================================

func TestApplySearchReplace_singleLineContent(t *testing.T) {
	got, err := ApplySearchReplace("hello", []SearchReplace{{Search: "hello", Replace: "world"}})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "world", got)
}

func TestApplySearchReplace_singleLineContentWSMatch(t *testing.T) {
	got, err := ApplySearchReplace("  hello", []SearchReplace{{Search: "hello", Replace: "world"}})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "  world", got)
}

func TestApplySearchReplace_contentOnlyNewlines(t *testing.T) {
	_, err := ApplySearchReplace("\n\n\n", []SearchReplace{{Search: "x", Replace: "y"}})
	if err == nil {
		t.Fatal("expected 'not found' error")
	}
}

func TestApplySearchReplace_searchOnFirstLine(t *testing.T) {
	got, err := ApplySearchReplace("target\nrest", []SearchReplace{{Search: "target", Replace: "hit"}})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "hit\nrest", got)
}

func TestApplySearchReplace_searchOnLastLine(t *testing.T) {
	got, err := ApplySearchReplace("first\nlast", []SearchReplace{{Search: "last", Replace: "final"}})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "first\nfinal", got)
}

// ============================================================================
// Content with special characters
// ============================================================================

func TestApplySearchReplace_specialChars(t *testing.T) {
	content := "cost: $100\ntax: 8%\npath: C:\\foo"
	ops := []SearchReplace{{Search: "cost: $100", Replace: "cost: $200"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "cost: $200\ntax: 8%\npath: C:\\foo", got)
}

func TestApplySearchReplace_emptyContent(t *testing.T) {
	_, err := ApplySearchReplace("", []SearchReplace{{Search: "x", Replace: "y"}})
	if err == nil {
		t.Fatal("expected error for search in empty content")
	}
}

func TestApplySearchReplace_contentEndsWithNewline(t *testing.T) {
	content := "a\nb\n"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: "b", Replace: "c"}})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nc\n", got)
}

func TestApplySearchReplace_contentEndsWithNewlineMultiLine(t *testing.T) {
	content := "a\nb\nc\n"
	ops := []SearchReplace{{Search: "b\nc", Replace: "x"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nx\n", got)
}

func TestApplySearchReplace_whitespaceContentLines(t *testing.T) {
	content := "a\n   \n\t\nb"
	ops := []SearchReplace{{Search: "a\n   \n\t\nb", Replace: "replaced"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "replaced", got)
}

// ============================================================================
// Substring within-line matching (Phase 5)
// ============================================================================

func TestApplySearchReplace_substringWithinLine(t *testing.T) {
	content := "add = lambda a, b: a + b"
	ops := []SearchReplace{{Search: "lambda a, b: a + b", Replace: "lambda a, b: a * b"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "add = lambda a, b: a * b", got)
}

func TestApplySearchReplace_substringWithinIndentedLine(t *testing.T) {
	content := "def foo():\n    result = x + y\n    return result"
	ops := []SearchReplace{{Search: "x + y", Replace: "x * y"}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "def foo():\n    result = x * y\n    return result", got)
}

func TestApplySearchReplace_substringWithFString(t *testing.T) {
	content := "name = \"world\"\nprint(f\"hello {name}\")\n"
	ops := []SearchReplace{{Search: `f"hello {name}"`, Replace: `f"goodbye {name}"`}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "name = \"world\"\nprint(f\"goodbye {name}\")\n", got)
}

func TestApplySearchReplace_substringDictKey(t *testing.T) {
	content := "config = {\"host\": \"localhost\", \"port\": 8080}\nprint(config[\"host\"])\n"
	ops := []SearchReplace{{Search: `"host": "localhost"`, Replace: `"host": "0.0.0.0"`}}
	got, err := ApplySearchReplace(content, ops)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "config = {\"host\": \"0.0.0.0\", \"port\": 8080}\nprint(config[\"host\"])\n", got)
}

// ============================================================================
// No-op / zero value
// ============================================================================

func TestApplySearchReplace_noOps(t *testing.T) {
	got, err := ApplySearchReplace("content", nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "content", got)
}

func TestApplySearchReplace_zeroValueOpSlice(t *testing.T) {
	got, err := ApplySearchReplace("content", []SearchReplace{})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "content", got)
}

// ============================================================================
// Python execution infrastructure
// ============================================================================

func runPython(t *testing.T, code string) string {
	t.Helper()
	dir := t.TempDir()
	fpath := filepath.Join(dir, "script.py")
	if err := os.WriteFile(fpath, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("python", fpath).Output()
	if err != nil {
		t.Fatalf("python execution failed:\ncode:\n%s\nerror: %v", code, err)
	}
	return strings.TrimRight(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
}

// ============================================================================
// Python: basic edits
// ============================================================================

func TestPython_editPrint(t *testing.T) {
	code := "print(\"hello\")\nprint(\"world\")\n"
	ops := []SearchReplace{{Search: `print("hello")`, Replace: `print("hi")`}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "hi\nworld", out)
}

func TestPython_editFunctionBody(t *testing.T) {
	code := "def greet():\n    name = \"Alice\"\n    print(\"Hello, \" + name)\n\ngreet()\n"
	ops := []SearchReplace{{
		Search:  `    name = "Alice"`,
		Replace: `    name = "Bob"`,
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "Hello, Bob", out)
}

func TestPython_editBiggerReplace(t *testing.T) {
	code := "x = 1\nprint(x)\n"
	ops := []SearchReplace{{
		Search:  "x = 1",
		Replace: "x = 1\ny = 2\nz = 3",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "1", out)
}

func TestPython_editSmallerReplace(t *testing.T) {
	code := "a = 10\nb = 20\nc = 30\nprint(a + c)\n"
	ops := []SearchReplace{{
		Search:  "a = 10\nb = 20",
		Replace: "a = 100",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "130", out)
}

func TestPython_removeVariableUsage(t *testing.T) {
	code := "items = [1, 2, 3, 4, 5]\nprint(sum(items))\n"
	ops := []SearchReplace{{
		Search:  "items = [1, 2, 3, 4, 5]",
		Replace: "items = [1, 2, 3]",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "6", out)
}

func TestPython_editSameIndentation(t *testing.T) {
	code := "def process():\n    total = 0\n    for i in range(5):\n        total += i\n    return total\n\nprint(process())\n"
	ops := []SearchReplace{{
		Search:  "    for i in range(5):\n        total += i",
		Replace: "    for i in range(10):\n        total += i",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "45", out)
}

func TestPython_editDifferentIndentation_wsMatch(t *testing.T) {
	code := "def process():\n    total = 0\n    for i in range(5):\n        total += i\n    return total\n\nprint(process())\n"
	ops := []SearchReplace{{
		Search:  "for i in range(5):\ntotal += i",
		Replace: "for i in range(3):\n    total += i",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "3", out)
}

func TestPython_wsMatchPreservesIndentation(t *testing.T) {
	code := "def outer():\n    def inner():\n        return 42\n    return inner()\n\nprint(outer())\n"
	ops := []SearchReplace{{
		Search:  "def inner():\n    return 42",
		Replace: "def inner():\n    return 99",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "99", out)
}

func TestPython_editLeadingBlankLineInSearch(t *testing.T) {
	code := "import sys\n\ndef main():\n    print(\"hello\")\n\nmain()\n"
	search := "\ndef main():\n    print(\"hello\")"
	ops := []SearchReplace{{
		Search:  search,
		Replace: "def main():\n    print(\"goodbye\")",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "goodbye", out)
}

func TestPython_sequentialEdits(t *testing.T) {
	code := "x = 1\ny = 2\nz = 3\nprint(x + y + z)\n"
	ops := []SearchReplace{
		{Search: "x = 1", Replace: "x = 10"},
		{Search: "y = 2", Replace: "y = 20"},
		{Search: "z = 3", Replace: "z = 30"},
	}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "60", out)
}

// ============================================================================
// Python: control flow edits
// ============================================================================

func TestPython_editConditionalLogic(t *testing.T) {
	code := "def check(n):\n    if n > 0:\n        return \"positive\"\n    return \"non-positive\"\n\nprint(check(5))\n"
	ops := []SearchReplace{{
		Search:  `        return "positive"`,
		Replace: `        return "POSITIVE"`,
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "POSITIVE", out)
}

func TestPython_editLoopBody(t *testing.T) {
	code := "result = []\nfor i in range(3):\n    result.append(i)\nprint(result)\n"
	ops := []SearchReplace{{
		Search:  "    result.append(i)",
		Replace: "    result.append(i * 2)",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "[0, 2, 4]", out)
}

func TestPython_editClassMethod(t *testing.T) {
	code := "class Calculator:\n    def add(self, a, b):\n        return a + b\n\ncalc = Calculator()\nprint(calc.add(3, 4))\n"
	ops := []SearchReplace{{
		Search:  "        return a + b",
		Replace: "        return a + b + 1",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "8", out)
}

func TestPython_editReplaceLineWithEmpty(t *testing.T) {
	code := "x = 1\ny = 2\nprint(x)\n"
	ops := []SearchReplace{{
		Search:  "y = 2",
		Replace: "",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "1", out)
}

func TestPython_editNestedIndentation(t *testing.T) {
	code := "def outer():\n    def inner():\n        if True:\n            return 1\n        return 2\n    return inner()\n\nprint(outer())\n"
	ops := []SearchReplace{{
		Search:  "        if True:\n            return 1\n        return 2",
		Replace: "        if False:\n            return 10\n        return 20",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "20", out)
}

func TestPython_editComment(t *testing.T) {
	code := "# compute sum\ntotal = sum(range(10))\nprint(total)\n"
	ops := []SearchReplace{{
		Search:  "# compute sum",
		Replace: "# compute sum (v2)",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "45", out)
}

func TestPython_editAddImport(t *testing.T) {
	code := "import os\n\nprint(os.name)\n"
	ops := []SearchReplace{{
		Search:  "import os",
		Replace: "import os\nimport sys",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	if out == "" {
		t.Error("python produced no output")
	}
}

func TestPython_editKeepSemanticsWhenSpacingChanges(t *testing.T) {
	code := "def add(a, b):\n    return a + b\n\nx = 1\ny = 2\nprint(add(x, y))\n"
	ops := []SearchReplace{{
		Search:  "    return a + b",
		Replace: "    return a * b",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "2", out)
}

func TestPython_editTabsToSpaces(t *testing.T) {
	code := "def foo():\n\tx = 1\n\ty = 2\n\tprint(x + y)\n\nfoo()\n"
	search := "x = 1\ny = 2"
	ops := []SearchReplace{{
		Search:  search,
		Replace: "x = 10\ny = 20",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "30", out)
}

func TestPython_editPreserveLogicAfterSearchTrim(t *testing.T) {
	code := "def greet(name):\n    return \"Hello, \" + name\n\nprint(greet(\"World\"))\n"
	search := "\n\ndef greet(name):\n    return \"Hello, \" + name\n\n"
	ops := []SearchReplace{{
		Search:  search,
		Replace: "def greet(name):\n    return \"Hi, \" + name",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "Hi, World", out)
}

func TestPython_editMultipleSameIndentBlocks(t *testing.T) {
	code := "def first():\n    x = 1\n    return x\n\ndef second():\n    x = 2\n    return x\n\nprint(first() + second())\n"
	ops := []SearchReplace{{
		Search:  "def second():\n    x = 2\n    return x",
		Replace: "def second():\n    x = 20\n    return x",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "21", out)
}

func TestPython_editComplexNested(t *testing.T) {
	code := "data = [1, 2, 3, 4, 5]\nresult = []\nfor x in data:\n    if x % 2 == 0:\n        result.append(x * 10)\n    else:\n        result.append(x)\nprint(result)\n"
	ops := []SearchReplace{{
		Search:  "        result.append(x * 10)\n    else:\n        result.append(x)",
		Replace: "        result.append(x * 100)\n    else:\n        result.append(x * -1)",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "[-1, 200, -3, 400, -5]", out)
}

func TestPython_editFuzzyMatchIndentDifferences(t *testing.T) {
	code := "def compute():\n    total = 0\n    for i in [1,2,3]:\n        total += i\n    return total\n\nprint(compute())\n"
	search := "total = 0\nfor i in [1,2,3]:\n    total += i"
	ops := []SearchReplace{{
		Search:  search,
		Replace: "total = 100\nfor i in [1,2,3]:\n    total += i",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "106", out)
}

// ============================================================================
// Python: substring matching inside lines
// ============================================================================

func TestPython_substringLambda(t *testing.T) {
	code := "add = lambda a, b: a + b\nprint(add(5, 7))\n"
	ops := []SearchReplace{{
		Search:  "lambda a, b: a + b",
		Replace: "lambda a, b: a * b",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "35", out)
}

func TestPython_substringListComp(t *testing.T) {
	code := "squares = [x*x for x in range(5)]\nprint(squares)\n"
	ops := []SearchReplace{{
		Search:  "[x*x for x in range(5)]",
		Replace: "[x*x for x in range(10)]",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "[0, 1, 4, 9, 16, 25, 36, 49, 64, 81]", out)
}

func TestPython_substringFString(t *testing.T) {
	code := "name = \"world\"\nprint(f\"hello {name}\")\n"
	ops := []SearchReplace{{
		Search:  `f"hello {name}"`,
		Replace: `f"goodbye {name}"`,
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "goodbye world", out)
}

func TestPython_substringDictKey(t *testing.T) {
	code := "config = {\"host\": \"localhost\", \"port\": 8080}\nprint(config[\"host\"])\n"
	ops := []SearchReplace{{
		Search:  `"host": "localhost"`,
		Replace: `"host": "0.0.0.0"`,
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "0.0.0.0", out)
}

func TestPython_substringGenerator(t *testing.T) {
	code := "total = sum(x*2 for x in range(5))\nprint(total)\n"
	ops := []SearchReplace{{
		Search:  "x*2",
		Replace: "x*3",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "30", out)
}

func TestPython_substringTypeAnnotation(t *testing.T) {
	code := "def greet(name: str) -> str:\n    return \"Hello, \" + name\n\nprint(greet(\"Alice\"))\n"
	ops := []SearchReplace{{
		Search:  "name: str) -> str:",
		Replace: "name: str) -> str:",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "Hello, Alice", out)
}

func TestPython_substringWalrusOperator(t *testing.T) {
	code := "values = [1, 2, 3, 4, 5]\nfiltered = [y for x in values if (y := x * 2) > 5]\nprint(filtered)\n"
	ops := []SearchReplace{{
		Search:  "(y := x * 2)",
		Replace: "(y := x * 3)",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "[6, 9, 12, 15]", out)
}

// ============================================================================
// Python: structural patterns (try/except, decorator, dataclass, closure)
// ============================================================================

func TestPython_editTryExcept(t *testing.T) {
	code := "def divide(a, b):\n    try:\n        return a // b\n    except ZeroDivisionError:\n        return -1\n\nprint(divide(10, 0))\n"
	ops := []SearchReplace{{
		Search:  "        return -1",
		Replace: "        return float('inf')",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "inf", out)
}

func TestPython_editDecorator(t *testing.T) {
	code := "def debug(func):\n    def wrapper(*args, **kwargs):\n        return func(*args, **kwargs)\n    return wrapper\n\n@debug\ndef say_hello():\n    return \"hello\"\n\nprint(say_hello())\n"
	ops := []SearchReplace{{
		Search:  `return "hello"`,
		Replace: `return "HELLO"`,
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "HELLO", out)
}

func TestPython_editDataclass(t *testing.T) {
	code := "from dataclasses import dataclass\n\n@dataclass\nclass Point:\n    x: int\n    y: int\n\np = Point(3, 4)\nprint(p.x + p.y)\n"
	ops := []SearchReplace{{
		Search:  "    x: int\n    y: int",
		Replace: "    x: int\n    y: int\n    z: int = 0",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "7", out)
}

func TestPython_editReplaceEntireFile(t *testing.T) {
	code := "x = 1\nprint(x)\n"
	ops := []SearchReplace{{
		Search:  "x = 1\nprint(x)",
		Replace: "print(42)",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "42", out)
}

func TestPython_editWithClosure(t *testing.T) {
	code := "def make_multiplier(n):\n    def multiplier(x):\n        return x * n\n    return multiplier\n\ndouble = make_multiplier(2)\nprint(double(5))\n"
	ops := []SearchReplace{{
		Search:  "        return x * n",
		Replace: "        return x * n * 10",
	}}
	got, err := ApplySearchReplace(code, ops)
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "100", out)
}

// ============================================================================
// TrimBlankEdges helper
// ============================================================================

func TestTrimBlankEdges(t *testing.T) {
	tests := []struct {
		input []string
		want  []string
	}{
		{[]string{"", "a", "b", ""}, []string{"a", "b"}},
		{[]string{"", "", "a", "", "b", "", ""}, []string{"a", "", "b"}},
		{[]string{" ", "\t", "a", " ", "b", "  "}, []string{"a", " ", "b"}},
		{[]string{"a", "b"}, []string{"a", "b"}},
		{[]string{"", "", ""}, nil},
		{nil, nil},
		{[]string{}, []string{}},
		{[]string{"", "a", ""}, []string{"a"}},
		{[]string{"\n", "\r", "x"}, []string{"x"}},
	}
	for _, tt := range tests {
		got := trimBlankEdges(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("trimBlankEdges(%q) = %q, want %q", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("trimBlankEdges(%q) = %q, want %q", tt.input, got, tt.want)
				break
			}
		}
	}
}

// ============================================================================
// DeleteLines tests
// ============================================================================

func TestDeleteLines_basicMiddleDeletion(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5"
	got, err := DeleteLines(content, "line2", "line4")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "line1\nline2\nline4\nline5", got)
}

func TestDeleteLines_multiLineAnchors(t *testing.T) {
	content := "a\nb\nc\nd\ne\nf\n"
	got, err := DeleteLines(content, "a\nb", "e\nf")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nb\ne\nf\n", got)
}

func TestDeleteLines_nothingBetween(t *testing.T) {
	content := "a\nb\nc"
	got, err := DeleteLines(content, "a", "b")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nb\nc", got)
}

func TestDeleteLines_emptyPrefix(t *testing.T) {
	_, err := DeleteLines("a\nb\nc", "", "b")
	if err == nil {
		t.Fatal("expected error for empty prefix")
	}
}

func TestDeleteLines_emptySuffix(t *testing.T) {
	_, err := DeleteLines("a\nb\nc", "a", "")
	if err == nil {
		t.Fatal("expected error for empty suffix")
	}
}

func TestDeleteLines_suffixBeforePrefix(t *testing.T) {
	_, err := DeleteLines("a\nb\nc", "b", "a")
	if err == nil {
		t.Fatal("expected error when suffix appears before prefix")
	}
}

func TestDeleteLines_withIndentation(t *testing.T) {
	content := "def foo():\n    x = 1\n    y = 2\n    z = 3\n    return z"
	got, err := DeleteLines(content, "    x = 1", "    z = 3")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "def foo():\n    x = 1\n    z = 3\n    return z", got)
}

func TestDeleteLines_singleLineContent(t *testing.T) {
	content := "onlyline"
	_, err := DeleteLines(content, "onlyline", "onlyline")
	if err == nil {
		t.Fatal("expected error: suffix must appear after prefix")
	}
}

func TestDeleteLines_anchorNotFound(t *testing.T) {
	_, err := DeleteLines("a\nb\nc", "x", "y")
	if err == nil {
		t.Fatal("expected error for not-found anchors")
	}
}

func TestDeleteLines_multipleMatches(t *testing.T) {
	_, err := DeleteLines("foo\nbar\nfoo\nbar", "foo", "bar")
	if err == nil {
		t.Fatal("expected error for ambiguous match")
	}
}

func TestDeleteLines_midLineAnchorRejected(t *testing.T) {
	content := "add = lambda a, b: a + b"
	_, err := DeleteLines(content, "lambda a, b: a + b", "add")
	if err == nil {
		t.Fatal("expected error: prefix starts mid-line")
	}
	if !strings.Contains(err.Error(), "LINE-LEVEL") {
		t.Errorf("error should mention LINE-LEVEL: %v", err)
	}
}

func TestDeleteLines_blockLevelOK(t *testing.T) {
	content := "import os\nimport sys\n\nprint('hello')"
	got, err := DeleteLines(content, "import sys", "\nprint")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "import os\nimport sys\nprint('hello')", got)
}

func TestDeleteLines_crlfContent(t *testing.T) {
	content := "line1\r\nline2\r\nline3\r\nline4"
	got, err := DeleteLines(content, "line2", "line3")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "line1\r\nline2\r\nline3\r\nline4", got)
}

// ============================================================================
// ReplaceLines tests
// ============================================================================

func TestReplaceLines_basicMiddleReplace(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5"
	got, err := ReplaceLines(content, "line2", "line4", "NEW")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "line1\nline2\nNEW\nline4\nline5", got)
}

func TestReplaceLines_multiLineReplace(t *testing.T) {
	content := "header\nbody1\nbody2\nbody3\nfooter"
	got, err := ReplaceLines(content, "header", "footer", "new body 1\nnew body 2")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "header\nnew body 1\nnew body 2\nfooter", got)
}

func TestReplaceLines_withIndentation(t *testing.T) {
	content := "def foo():\n    x = 1\n    y = 2\n    z = 3\n    return z"
	got, err := ReplaceLines(content, "    x = 1", "    z = 3", "    a = 10\n    b = 20")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "def foo():\n    x = 1\n    a = 10\n    b = 20\n    z = 3\n    return z", got)
}

func TestReplaceLines_emptyNewText(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5"
	got, err := ReplaceLines(content, "line2", "line4", "")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "line1\nline2\nline4\nline5", got)
}

func TestReplaceLines_midLineAnchorRejected(t *testing.T) {
	content := "cfg = {\"host\": \"localhost\"}"
	_, err := ReplaceLines(content, "\"host\": \"localhost\"", "cfg", "new")
	if err == nil {
		t.Fatal("expected error: prefix starts mid-line")
	}
	if !strings.Contains(err.Error(), "LINE-LEVEL") {
		t.Errorf("error should mention LINE-LEVEL: %v", err)
	}
}

func TestReplaceLines_emptyPrefix(t *testing.T) {
	_, err := ReplaceLines("a\nb\nc", "", "b", "x")
	if err == nil {
		t.Fatal("expected error for empty prefix")
	}
}

// ============================================================================
// Python: DeleteLines execution tests
// ============================================================================

func TestPython_deleteLines_functionBody(t *testing.T) {
	code := "total = 0\n#comment\nfor i in range(5):\n    total += i\n\nprint(total)\n"
	got, err := DeleteLines(code, "total = 0", "for i in range(5):\n    total += i")
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "10", out)
}

func TestPython_deleteLines_singleStatement(t *testing.T) {
	code := "x = 1\ny = 2\nz = 3\nprint(x + z)\n"
	got, err := DeleteLines(code, "x = 1", "z = 3")
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "4", out)
}

func TestPython_replaceLines_functionBody(t *testing.T) {
	code := "def compute():\n    total = 0\n    for i in range(5):\n        total += i\n    return total\n\nprint(compute())\n"
	got, err := ReplaceLines(code, "    total = 0", "    return total", "    return 99")
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	assertEqual(t, "99", out)
}

func TestPython_replaceLines_addImport(t *testing.T) {
	code := "import os\n\nprint(os.name)\n"
	got, err := ReplaceLines(code, "import os", "print", "import os\nimport sys")
	if err != nil {
		t.Fatal(err)
	}
	out := runPython(t, got)
	if out == "" {
		t.Error("python produced no output")
	}
}

// ============================================================================
// Absolute indentation re-indentation (the mid-block edit bug)
// ============================================================================

func TestApplySearchReplace_absoluteIndentTabBlock(t *testing.T) {
	// Regression test: model provides old_string starting at column 0 (mid-block)
	// but continuation lines have absolute file indentation. The reindent function
	// must NOT double the indentation on those continuation lines.
	content := "func foo() {\n\t\t\t\tsegments := hardWrap(logStr, textW)\n\n\t\t\t\tfor _, seg := range segments {\n\t\t\t\t\tprefix := \"❯ \"\n\t\t\t\t}"
	search := "segments := hardWrap(logStr, textW)\n\n\t\t\t\tfor _, seg := range segments {\n\t\t\t\t\tprefix := \"❯ \"\n\t\t\t\t}"
	replace := "segments := wordWrap(logStr, textW)\n\n\t\t\t\tfor _, seg := range segments {\n\t\t\t\t\tprefix := \"❯ \"\n\t\t\t\t}"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}})
	if err != nil {
		t.Fatal(err)
	}
	want := "func foo() {\n\t\t\t\tsegments := wordWrap(logStr, textW)\n\n\t\t\t\tfor _, seg := range segments {\n\t\t\t\t\tprefix := \"❯ \"\n\t\t\t\t}"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

func TestApplySearchReplace_absoluteIndentSpaceBlock(t *testing.T) {
	// Same bug with spaces instead of tabs.
	content := "def foo():\n    x = process(a)\n\n    for i in items:\n        print(i)"
	search := "x = process(a)\n\n    for i in items:\n        print(i)"
	replace := "x = process(b)\n\n    for i in items:\n        print(i)"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}})
	if err != nil {
		t.Fatal(err)
	}
	want := "def foo():\n    x = process(b)\n\n    for i in items:\n        print(i)"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

func TestApplySearchReplace_relativeIndentStillWorks(t *testing.T) {
	// Ensure that relative indentation (where model provides continuation lines
	// with only relative depth) still gets upgraded correctly.
	content := "def compute():\n    total = 0\n    for i in [1,2,3]:\n        total += i\n    return total\n\nprint(compute())\n"
	search := "total = 0\nfor i in [1,2,3]:\n    total += i"
	replace := "total = 100\nfor i in [1,2,3]:\n    total += i"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}})
	if err != nil {
		t.Fatal(err)
	}
	want := "def compute():\n    total = 100\n    for i in [1,2,3]:\n        total += i\n    return total\n\nprint(compute())\n"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

func TestApplySearchReplace_absoluteIndentSingleLineReplace(t *testing.T) {
	// Single-line WS-normalized replacement at column 0 should still get mi added.
	content := "func foo() {\n\t\t\t\tresult := compute()\n\t\t}"
	search := "result := compute()"
	replace := "result := process()"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}})
	if err != nil {
		t.Fatal(err)
	}
	want := "func foo() {\n\t\t\t\tresult := process()\n\t\t}"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

// ============================================================================
// assertEqual helper
// ============================================================================

func assertEqual(t *testing.T, want, got string) {
	t.Helper()
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}
