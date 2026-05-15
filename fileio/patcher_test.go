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
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "line1\nreplaced\nline3", got)
}

func TestApplySearchReplace_multiLineExact(t *testing.T) {
	content := "a\nb\nc\nd\ne"
	ops := []SearchReplace{{Search: "b\nc\nd", Replace: "x\ny"}}
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nx\ny\ne", got)
}

func TestApplySearchReplace_searchEqualsReplace(t *testing.T) {
	content := "hello\nworld"
	ops := []SearchReplace{{Search: "world", Replace: "world"}}
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, content, got)
}

func TestApplySearchReplace_emptySearch(t *testing.T) {
	_, err := ApplySearchReplace("hello", []SearchReplace{{Search: "", Replace: "x"}}, nil)
	if err == nil {
		t.Fatal("expected error for empty search")
	}
}

func TestApplySearchReplace_searchNotFound(t *testing.T) {
	_, err := ApplySearchReplace("hello\nworld", []SearchReplace{{Search: "missing", Replace: "x"}}, nil)
	if err == nil {
		t.Fatal("expected error for not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApplySearchReplace_multipleExactMatches(t *testing.T) {
	content := "foo\nbar\nfoo\nbar"
	_, err := ApplySearchReplace(content, []SearchReplace{{Search: "foo\nbar", Replace: "x"}}, nil)
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
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "  foo\n  bar\nreplaced", got)
}

func TestApplySearchReplace_multipleWSMatchesError(t *testing.T) {
	content := "  a\n  b\na\n  b"
	_, err := ApplySearchReplace(content, []SearchReplace{{Search: "a\nb", Replace: "x"}}, nil)
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
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "header\n\ndef new_func():\n    pass", got)
}

func TestApplySearchReplace_trailingNewlineInSearch(t *testing.T) {
	content := "line1\nline2\nline3"
	search := "line2\n"
	ops := []SearchReplace{{Search: search, Replace: "replaced"}}
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "line1\nreplaced\nline3", got)
}

func TestApplySearchReplace_leadingAndTrailingBlankSearch(t *testing.T) {
	content := "a\n\n\nb\n\nc"
	search := "\n\nb\n\n"
	ops := []SearchReplace{{Search: search, Replace: "B"}}
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\n\n\nB\n\nc", got)
}

func TestApplySearchReplace_searchIsOnlyBlankLines(t *testing.T) {
	content := "a\n\n\nb"
	search := " \n\n\t"
	_, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: "x"}}, nil)
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
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nx\ne", got)
}

func TestApplySearchReplace_replaceBigger(t *testing.T) {
	content := "a\nb\ne"
	ops := []SearchReplace{{Search: "b", Replace: "c\nd\ne\nf"}}
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nc\nd\ne\nf\ne", got)
}

func TestApplySearchReplace_replaceToEmpty(t *testing.T) {
	content := "a\nb\nc"
	ops := []SearchReplace{{Search: "b", Replace: ""}}
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\n\nc", got)
}

func TestApplySearchReplace_replaceToEmpty_multiline(t *testing.T) {
	content := "a\nb\nc\nd"
	ops := []SearchReplace{{Search: "b\nc", Replace: ""}}
	got, err := ApplySearchReplace(content, ops, nil)
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
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "def foo():\n    a = 10\n    b = 20\n    return x", got)
}

func TestApplySearchReplace_differentIndentation_wsMatch(t *testing.T) {
	content := "def foo():\n    x = 1\n    y = 2"
	search := "x = 1\ny = 2"
	ops := []SearchReplace{{Search: search, Replace: "a = 10\nb = 20"}}
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "def foo():\n    a = 10\n    b = 20", got)
}

func TestApplySearchReplace_tabIndentation(t *testing.T) {
	content := "def foo():\n\tx = 1\n\ty = 2"
	search := "x = 1\ny = 2"
	ops := []SearchReplace{{Search: search, Replace: "a = 10"}}
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "def foo():\n\ta = 10", got)
}

func TestApplySearchReplace_mixedIndentation_tabToSpace(t *testing.T) {
	content := "def foo():\n    x = 1\n    y = 2"
	search := "\tx = 1\n\ty = 2"
	ops := []SearchReplace{{Search: search, Replace: "a = 1"}}
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "def foo():\n    a = 1", got)
}

func TestApplySearchReplace_mixedIndentation_spaceToTab(t *testing.T) {
	content := "def foo():\n\tx = 1\n\ty = 2"
	search := "    x = 1\n    y = 2"
	ops := []SearchReplace{{Search: search, Replace: "a = 1"}}
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "def foo():\n\ta = 1", got)
}

func TestApplySearchReplace_reindentDeeperLevel(t *testing.T) {
	content := "def outer():\n        x = 1\n        y = 2"
	search := "  x = 1\n  y = 2"
	ops := []SearchReplace{{Search: search, Replace: "  a = 10\n  b = 20"}}
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "def outer():\n        a = 10\n        b = 20", got)
}

func TestApplySearchReplace_reindentShallowerLevel(t *testing.T) {
	content := "if True:\n  x = 1\n  y = 2"
	search := "        x = 1\n        y = 2"
	ops := []SearchReplace{{Search: search, Replace: "        a = 10\n        b = 20"}}
	got, err := ApplySearchReplace(content, ops, nil)
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
	got, err := ApplySearchReplace(content, ops, nil)
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
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "x\ny\nc", got)
}

func TestApplySearchReplace_mixedCrlfLf(t *testing.T) {
	content := "a\r\nb\nc\r\n"
	ops := []SearchReplace{{Search: "b", Replace: "X"}}
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\r\nX\r\nc\r\n", got)
}

func TestApplySearchReplace_lfContent_staysLf(t *testing.T) {
	content := "a\nb\nc"
	ops := []SearchReplace{{Search: "b", Replace: "X"}}
	got, err := ApplySearchReplace(content, ops, nil)
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
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nX\nY\ne", got)
}

func TestApplySearchReplace_sequentialFirstFails(t *testing.T) {
	_, err := ApplySearchReplace("a\nb", []SearchReplace{
		{Search: "missing", Replace: "X"},
		{Search: "a", Replace: "Y"},
	}, nil)
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
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nB", got)
}

// ============================================================================
// Single-line edge cases
// ============================================================================

func TestApplySearchReplace_singleLineContent(t *testing.T) {
	got, err := ApplySearchReplace("hello", []SearchReplace{{Search: "hello", Replace: "world"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "world", got)
}

func TestApplySearchReplace_singleLineContentWSMatch(t *testing.T) {
	got, err := ApplySearchReplace("  hello", []SearchReplace{{Search: "hello", Replace: "world"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "  world", got)
}

func TestApplySearchReplace_contentOnlyNewlines(t *testing.T) {
	_, err := ApplySearchReplace("\n\n\n", []SearchReplace{{Search: "x", Replace: "y"}}, nil)
	if err == nil {
		t.Fatal("expected 'not found' error")
	}
}

func TestApplySearchReplace_searchOnFirstLine(t *testing.T) {
	got, err := ApplySearchReplace("target\nrest", []SearchReplace{{Search: "target", Replace: "hit"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "hit\nrest", got)
}

func TestApplySearchReplace_searchOnLastLine(t *testing.T) {
	got, err := ApplySearchReplace("first\nlast", []SearchReplace{{Search: "last", Replace: "final"}}, nil)
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
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "cost: $200\ntax: 8%\npath: C:\\foo", got)
}

func TestApplySearchReplace_emptyContent(t *testing.T) {
	_, err := ApplySearchReplace("", []SearchReplace{{Search: "x", Replace: "y"}}, nil)
	if err == nil {
		t.Fatal("expected error for search in empty content")
	}
}

func TestApplySearchReplace_contentEndsWithNewline(t *testing.T) {
	content := "a\nb\n"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: "b", Replace: "c"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nc\n", got)
}

func TestApplySearchReplace_contentEndsWithNewlineMultiLine(t *testing.T) {
	content := "a\nb\nc\n"
	ops := []SearchReplace{{Search: "b\nc", Replace: "x"}}
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a\nx\n", got)
}

func TestApplySearchReplace_whitespaceContentLines(t *testing.T) {
	content := "a\n   \n\t\nb"
	ops := []SearchReplace{{Search: "a\n   \n\t\nb", Replace: "replaced"}}
	got, err := ApplySearchReplace(content, ops, nil)
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
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "add = lambda a, b: a * b", got)
}

func TestApplySearchReplace_substringWithinIndentedLine(t *testing.T) {
	content := "def foo():\n    result = x + y\n    return result"
	ops := []SearchReplace{{Search: "x + y", Replace: "x * y"}}
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "def foo():\n    result = x * y\n    return result", got)
}

func TestApplySearchReplace_substringWithFString(t *testing.T) {
	content := "name = \"world\"\nprint(f\"hello {name}\")\n"
	ops := []SearchReplace{{Search: `f"hello {name}"`, Replace: `f"goodbye {name}"`}}
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "name = \"world\"\nprint(f\"goodbye {name}\")\n", got)
}

func TestApplySearchReplace_substringDictKey(t *testing.T) {
	content := "config = {\"host\": \"localhost\", \"port\": 8080}\nprint(config[\"host\"])\n"
	ops := []SearchReplace{{Search: `"host": "localhost"`, Replace: `"host": "0.0.0.0"`}}
	got, err := ApplySearchReplace(content, ops, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "config = {\"host\": \"0.0.0.0\", \"port\": 8080}\nprint(config[\"host\"])\n", got)
}

// ============================================================================
// No-op / zero value
// ============================================================================

func TestApplySearchReplace_noOps(t *testing.T) {
	got, err := ApplySearchReplace("content", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "content", got)
}

func TestApplySearchReplace_zeroValueOpSlice(t *testing.T) {
	got, err := ApplySearchReplace("content", []SearchReplace{}, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
	got, err := ApplySearchReplace(code, ops, nil)
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
// Absolute indentation re-indentation (the mid-block edit bug)
// ============================================================================

func TestApplySearchReplace_absoluteIndentTabBlock(t *testing.T) {
	// Regression test: model provides old_string starting at column 0 (mid-block)
	// but continuation lines have absolute file indentation. The reindent function
	// must NOT double the indentation on those continuation lines.
	content := "func foo() {\n\t\t\t\tsegments := hardWrap(logStr, textW)\n\n\t\t\t\tfor _, seg := range segments {\n\t\t\t\t\tprefix := \"❯ \"\n\t\t\t\t}"
	search := "segments := hardWrap(logStr, textW)\n\n\t\t\t\tfor _, seg := range segments {\n\t\t\t\t\tprefix := \"❯ \"\n\t\t\t\t}"
	replace := "segments := wordWrap(logStr, textW)\n\n\t\t\t\tfor _, seg := range segments {\n\t\t\t\t\tprefix := \"❯ \"\n\t\t\t\t}"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}}, nil)
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
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}}, nil)
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
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}}, nil)
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
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "func foo() {\n\t\t\t\tresult := process()\n\t\t}"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

func TestApplySearchReplace_mixedIndentLevelsAcrossBlock(t *testing.T) {
	// Regression: search spans from inside a block to the top level.
	// The closing } is at \t indent, and a blank line + top-level func follow.
	// The \t} in the search matches \t} in the file (absolute), so the
	// replacement } at column 0 should be re-indented to \t.
	// The blank line stays blank. The top-level func line stays at column 0.
	// Note: the search "func" line only matches as substring, so the full
	// content line (with signature) gets replaced by just the search text.
	content := "func foo() {\n\treturn i\n\t}\n\nfunc bar() {"
	search := "return i\n\t}\n\nfunc bar"
	replace := "return i\n}\n\nfunc bar"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// } gets re-indented from col 0 to \t (matching content's \t).
	// func bar stays at col 0 (both search and content are at col 0 — absolute).
	// But since splice replaces 4 lines including the full content line "func bar() {",
	// and the replacement only has "func bar", the signature is lost.
	want := "func foo() {\n\treturn i\n\t}\n\nfunc bar"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

func TestApplySearchReplace_mixedIndentTopLevelFuncNotDoubled(t *testing.T) {
	// Search spans from indented code to top-level function.
	// The top-level line must NOT get the block indent prepended.
	content := "if true {\n\t\tdoStuff()\n\t}\n\nfunc next() {"
	search := "doStuff()\n\t}\n\nfunc next"
	replace := "doOther()\n}\n\nfunc next"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// doOther() at col 0 → gets \t\t (matching content's \t\t)
	// } at col 0 → gets \t (matching content's \t from \t})
	// blank line stays blank
	// func next at col 0 → stays at col 0 (both search and content are at col 0)
	want := "if true {\n\t\tdoOther()\n\t}\n\nfunc next"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

// ============================================================================
// Space-to-tab reindent regression (the cascade corruption bug)
// ============================================================================

func TestApplySearchReplace_spaceSearchTabFile_regression(t *testing.T) {
	content := "func foo() {\n\t\t\tif evt.Type == \"usage\" {\n\t\t\t\tmodel := activeCfg.Model\n\t\t\t\tvar promptT, compT, cachedT int\n\t\t\t\tswitch d := evt.Data.(type) {\n\t\t\t\tcase map[string]int:\n\t\t\t\t\tpromptT = d[\"prompt\"]\n\t\t\t\t}\n\t\t\t\tif promptT > 0 || compT > 0 {\n\t\t\t\t\ts.cost.RecordUsage(model, promptT, compT, cachedT)\n\t\t\t\t}\n\t\t\t}"
	search := "        if evt.Type == \"usage\" {\n            model := activeCfg.Model\n            var promptT, compT, cachedT int\n            switch d := evt.Data.(type) {\n            case map[string]int:\n                promptT = d[\"prompt\"]\n            }\n            if promptT > 0 || compT > 0 {\n                s.cost.RecordUsage(model, promptT, compT, cachedT)\n            }"
	replace := "        if evt.Type == \"usage\" {\n            model := activeCfg.Model\n            var promptT, compT, cachedT int\n            isEstimated := false\n            switch d := evt.Data.(type) {\n            case map[string]int:\n                promptT = d[\"prompt\"]\n            }\n            if promptT > 0 || compT > 0 {\n                s.cost.RecordUsage(model, promptT, compT, cachedT)\n            }"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "func foo() {\n\t\t\tif evt.Type == \"usage\" {\n\t\t\t\tmodel := activeCfg.Model\n\t\t\t\tvar promptT, compT, cachedT int\n\t\t\t\tisEstimated := false\n\t\t\t\tswitch d := evt.Data.(type) {\n\t\t\t\tcase map[string]int:\n\t\t\t\t\tpromptT = d[\"prompt\"]\n\t\t\t\t}\n\t\t\t\tif promptT > 0 || compT > 0 {\n\t\t\t\t\ts.cost.RecordUsage(model, promptT, compT, cachedT)\n\t\t\t\t}\n\t\t\t}"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

func TestApplySearchReplace_tabSearchSpaceFile_regression(t *testing.T) {
	content := "def foo():\n        if evt.type == \"usage\":\n            model = active_cfg.model\n            var_t = d[\"prompt\"]\n        }"
	search := "\tif evt.type == \"usage\":\n\t\tmodel = active_cfg.model\n\t\tvar_t = d[\"prompt\"]\n\t}"
	replace := "\tif evt.type == \"usage\":\n\t\tmodel = active_cfg.model\n\t\tis_estimated = False\n\t\tvar_t = d[\"prompt\"]\n\t}"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "def foo():\n        if evt.type == \"usage\":\n            model = active_cfg.model\n            is_estimated = False\n            var_t = d[\"prompt\"]\n        }"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

// ============================================================================
// Absolute-line re-indent corruption (the mixedIndent si-based adjustment bug)
// ============================================================================

func TestApplySearchReplace_mixedIndentAbsoluteLineNotCorrupted(t *testing.T) {
	content := "\t\ttotalPromptTokens += promptTokens\n\t\ttotalCompletionTokens += completionTokens\n\t\tlastPromptTokens = promptTokens\n\n\t\tif promptTokens > 0 || completionTokens > 0 {\n\t\t\t// Compute cost for this turn using model pricing\n\t\t\tprice := provider.LookupPrice(a.Model)\n\t\t\tturnCost := price.CalculateCost(promptTokens, completionTokens, promptTokensCached)\n\t\t\temit(AgentEvent{Type: \"usage\", Data: map[string]any{\n\t\t\t\t\"prompt\":         promptTokens,\n\t\t\t\t\"completion\":     completionTokens,\n\t\t\t}})\n\t\t}"
	search := "\ttotalPromptTokens += promptTokens\n\t\ttotalCompletionTokens += completionTokens\n\t\tlastPromptTokens = promptTokens\n\n\t\tif promptTokens > 0 || completionTokens > 0 {\n\t\t\t// Compute cost for this turn using model pricing\n\t\t\tprice := provider.LookupPrice(a.Model)\n\t\t\tturnCost := price.CalculateCost(promptTokens, completionTokens, promptTokensCached)\n\t\t\temit(AgentEvent{Type: \"usage\", Data: map[string]any{\n\t\t\t\t\"prompt\":         promptTokens,\n\t\t\t\t\"completion\":     completionTokens,\n\t\t\t}})\n\t\t}"
	replace := "\ttotalPromptTokens += promptTokens\n\t\ttotalCompletionTokens += completionTokens\n\t\tlastPromptTokens = promptTokens\n\n\t\t// Always emit a usage event so the TUI status bar updates.\n\t\t// When the API doesn't return token counts, estimate tokens.\n\t\testimated := false\n\t\tif promptTokens == 0 && completionTokens == 0 {\n\t\t\tpromptTokens = provider.EstimateMessagesTotal(msgs, a.Model)\n\t\t\tcompletionTokens = provider.EstimateTokens(content, a.Model)\n\t\t\testimated = true\n\t\t}\n\n\t\t// Compute cost for this turn using model pricing\n\t\tprice := provider.LookupPrice(a.Model)\n\t\tturnCost := price.CalculateCost(promptTokens, completionTokens, promptTokensCached)\n\t\temit(AgentEvent{Type: \"usage\", Data: map[string]any{\n\t\t\t\"prompt\":         promptTokens,\n\t\t\t\"completion\":     completionTokens,\n\t\t\t\"estimated\":      estimated,\n\t\t}})"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "\t\ttotalPromptTokens += promptTokens\n\t\ttotalCompletionTokens += completionTokens\n\t\tlastPromptTokens = promptTokens\n\n\t\t// Always emit a usage event so the TUI status bar updates.\n\t\t// When the API doesn't return token counts, estimate tokens.\n\t\testimated := false\n\t\tif promptTokens == 0 && completionTokens == 0 {\n\t\t\tpromptTokens = provider.EstimateMessagesTotal(msgs, a.Model)\n\t\t\tcompletionTokens = provider.EstimateTokens(content, a.Model)\n\t\t\testimated = true\n\t\t}\n\n\t\t// Compute cost for this turn using model pricing\n\t\tprice := provider.LookupPrice(a.Model)\n\t\tturnCost := price.CalculateCost(promptTokens, completionTokens, promptTokensCached)\n\t\temit(AgentEvent{Type: \"usage\", Data: map[string]any{\n\t\t\t\"prompt\":         promptTokens,\n\t\t\t\"completion\":     completionTokens,\n\t\t\t\"estimated\":      estimated,\n\t\t}})"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

func TestApplySearchReplace_mixedIndentReplacementDeeperThanContent(t *testing.T) {
	content := "def outer():\n    x = 1\n    y = 2"
	search := "x = 1\n    y = 2"
	replace := "x = 1\n    if cond:\n        y = 2"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "def outer():\n    x = 1\n    if cond:\n        y = 2"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

func TestApplySearchReplace_mixedIndentReplacementShallowerThanContent(t *testing.T) {
	content := "def outer():\n    if cond:\n        x = 1\n        y = 2"
	search := "x = 1\n        y = 2"
	replace := "x = 1\n    y = 2"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "def outer():\n    if cond:\n        x = 1\n    y = 2"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

func TestApplySearchReplace_mixedIndentNewLinesNotDoubled(t *testing.T) {
	content := "def outer():\n    x = process(a)\n\n    for i in items:\n        print(i)"
	search := "x = process(a)\n\n    for i in items:\n        print(i)"
	replace := "x = process(b)\n    is_cached = False\n\n    for i in items:\n        print(i)"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "def outer():\n    x = process(b)\n    is_cached = False\n\n    for i in items:\n        print(i)"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

func TestApplySearchReplace_mixedIndentSessionGoExactScenario(t *testing.T) {
	content := "\t\tif evt.Type == \"usage\" {\n\t\t\tmodel := activeCfg.Model\n\t\t\tvar promptT, compT, cachedT int\n\t\t\tswitch d := evt.Data.(type) {\n\t\t\tcase map[string]int:\n\t\t\t\tpromptT = d[\"prompt\"]\n\t\t\t\tcompT = d[\"completion\"]\n\t\t\t\tcachedT = d[\"prompt_cached\"]\n\t\t\tcase map[string]any:\n\t\t\t\tif v, ok := d[\"prompt\"].(float64); ok {\n\t\t\t\t\tpromptT = int(v)\n\t\t\t\t}\n\t\t\t}\n\t\t\tif promptT > 0 || compT > 0 {\n\t\t\t\ts.cost.RecordUsage(model, promptT, compT, cachedT)\n\t\t\t}\n\t\t}"
	search := "if evt.Type == \"usage\" {\n\t\t\tmodel := activeCfg.Model\n\t\t\tvar promptT, compT, cachedT int\n\t\t\tswitch d := evt.Data.(type) {\n\t\t\tcase map[string]int:\n\t\t\t\tpromptT = d[\"prompt\"]\n\t\t\t\tcompT = d[\"completion\"]\n\t\t\t\tcachedT = d[\"prompt_cached\"]\n\t\t\tcase map[string]any:\n\t\t\t\tif v, ok := d[\"prompt\"].(float64); ok {\n\t\t\t\t\tpromptT = int(v)\n\t\t\t\t}\n\t\t\t}\n\t\t\tif promptT > 0 || compT > 0 {\n\t\t\t\ts.cost.RecordUsage(model, promptT, compT, cachedT)\n\t\t\t}\n\t\t}"
	replace := "if evt.Type == \"usage\" {\n\t\t\tmodel := activeCfg.Model\n\t\t\tvar promptT, compT, cachedT int\n\t\t\tisEstimated := false\n\t\t\tswitch d := evt.Data.(type) {\n\t\t\tcase map[string]int:\n\t\t\t\tpromptT = d[\"prompt\"]\n\t\t\t\tcompT = d[\"completion\"]\n\t\t\t\tcachedT = d[\"prompt_cached\"]\n\t\t\tcase map[string]any:\n\t\t\t\tif v, ok := d[\"prompt\"].(float64); ok {\n\t\t\t\t\tpromptT = int(v)\n\t\t\t\t}\n\t\t\t\tif v, ok := d[\"estimated\"].(bool); ok {\n\t\t\t\t\tisEstimated = v\n\t\t\t\t}\n\t\t\t}\n\t\t\tif promptT > 0 || compT > 0 {\n\t\t\t\ts.cost.RecordUsage(model, promptT, compT, cachedT)\n\t\t\t}\n\t\t}"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "\t\tif evt.Type == \"usage\" {\n\t\t\tmodel := activeCfg.Model\n\t\t\tvar promptT, compT, cachedT int\n\t\t\tisEstimated := false\n\t\t\tswitch d := evt.Data.(type) {\n\t\t\tcase map[string]int:\n\t\t\t\tpromptT = d[\"prompt\"]\n\t\t\t\tcompT = d[\"completion\"]\n\t\t\t\tcachedT = d[\"prompt_cached\"]\n\t\t\tcase map[string]any:\n\t\t\t\tif v, ok := d[\"prompt\"].(float64); ok {\n\t\t\t\t\tpromptT = int(v)\n\t\t\t\t}\n\t\t\t\tif v, ok := d[\"estimated\"].(bool); ok {\n\t\t\t\t\tisEstimated = v\n\t\t\t\t}\n\t\t\t}\n\t\t\tif promptT > 0 || compT > 0 {\n\t\t\t\ts.cost.RecordUsage(model, promptT, compT, cachedT)\n\t\t\t}\n\t\t}"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

func TestApplySearchReplace_mixedIndentRemovingNesting(t *testing.T) {
	content := "func handler() {\n\t\tif debug {\n\t\t\tlog.Printf(\"msg\")\n\t\t\tlog.Printf(\"detail\")\n\t\t}\n\t}"
	search := "if debug {\n\t\t\tlog.Printf(\"msg\")\n\t\t\tlog.Printf(\"detail\")\n\t\t}"
	replace := "if debug {\n\t\tlog.Printf(\"msg\")\n\t}"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "func handler() {\n\t\tif debug {\n\t\tlog.Printf(\"msg\")\n\t}\n\t}"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

func TestApplySearchReplace_mixedIndentAddLineAtAbsoluteDepth(t *testing.T) {
	content := "\t\tfor i := range items {\n\t\t\tval := items[i]\n\t\t\tresult = append(result, val)\n\t\t}"
	search := "for i := range items {\n\t\t\tval := items[i]\n\t\t\tresult = append(result, val)\n\t\t}"
	replace := "for i := range items {\n\t\t\tval := items[i]\n\t\t\tif val > 0 {\n\t\t\t\tresult = append(result, val)\n\t\t\t}\n\t\t}"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: search, Replace: replace}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "\t\tfor i := range items {\n\t\t\tval := items[i]\n\t\t\tif val > 0 {\n\t\t\t\tresult = append(result, val)\n\t\t\t}\n\t\t}"
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
