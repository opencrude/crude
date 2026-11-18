// Adapted for Crude from fgn/applypatch 95ed66f6d36a575b00001f2e27aedc1ec7455812.
// See NOTICE for provenance and modification details.
package codexio

import (
	"maps"
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()
	update := func(path, move, diff string) Operation {
		return Operation{Kind: UpdateFile, Path: path, MoveTo: move, Diff: diff}
	}
	for _, tc := range []struct {
		name, patch string
		want        *Patch
	}{
		{
			"all operations",
			"*** Begin Patch\n*** Add File: a.txt\n+one\n+\n*** Delete File: b.txt\n" +
				"*** Update File: c.txt\n*** Move to: d.txt\n@@ x\n-y\n+z\n*** End Patch",
			&Patch{Operations: []Operation{
				{Kind: AddFile, Path: "a.txt", Contents: "one\n\n"},
				{Kind: DeleteFile, Path: "b.txt"},
				update("c.txt", "d.txt", "@@ x\n-y\n+z\n"),
			}},
		},
		{
			"environment ID and an empty add",
			"*** Begin Patch\n*** Environment ID:  env \n*** Add File: a.txt\n*** End Patch",
			&Patch{EnvironmentID: "env", Operations: []Operation{{Kind: AddFile, Path: "a.txt"}}},
		},
		{
			"heredoc, CRLF, and padded markers",
			"<<'EOF'\r\n  *** Begin Patch \r\n*** Update File: a.txt  \r\n-x\r\n *** End Patch\r\nEOF\r\n",
			&Patch{Operations: []Operation{update("a.txt", "", "-x\n")}},
		},
		{
			"indented markers inside an update are diff lines",
			"*** Begin Patch\n*** Update File: a.txt\n-x\n  *** Add File: b.txt\n*** End Patch",
			&Patch{Operations: []Operation{update("a.txt", "", "-x\n  *** Add File: b.txt\n")}},
		},
		{
			"move after a leading end of file",
			"*** Begin Patch\n*** Update File: a.txt\n*** End of File\n*** Move to: b.txt\n-x\n*** End Patch",
			&Patch{Operations: []Operation{update("a.txt", "b.txt", "*** End of File\n-x\n")}},
		},
		{
			"carriage returns are removed twice at most",
			"*** Begin Patch\n*** Update File: a.txt\n-x\r\r\r\n*** End Patch",
			&Patch{Operations: []Operation{update("a.txt", "", "-x\r\n")}},
		},
		{
			"blank lines after the end",
			"*** Begin Patch\n*** Delete File: a.txt\n*** End Patch\n\n  \n*** End Patch",
			&Patch{Operations: []Operation{{Kind: DeleteFile, Path: "a.txt"}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(tc.patch)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, %v\nwant %#v", got, err, tc.want)
			}
		})
	}
}

// envelope wraps lines in "*** Begin Patch" and "*** End Patch".
func envelope(lines ...string) string {
	return "*** Begin Patch\n" + strings.Join(append(lines, "*** End Patch"), "\n")
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, patch, err string }{
		{"empty", " \n ", "first line of the patch must be '*** Begin Patch'"},
		{"no begin", "*** Add File: a\n*** End Patch", "first line of the patch must be '*** Begin Patch'"},
		{"no end", "*** Begin Patch\n*** Add File: a", "last line of the patch must be '*** End Patch'"},
		{"bad heredoc", "<<EOF\n*** Begin Patch\n*** End Patch\nEOX", "first line of the patch must be"},
		{"short heredoc", "<<EOF\n*** Begin Patch\nEOF", "first line of the patch must be"},
		{"unknown heredoc marker", "<<X\n*** Begin Patch\n*** End Patch\nEOF", "first line of the patch must be"},
		{"heredoc without end", "<<EOF\n*** Begin Patch\n+a\nEOF", "last line of the patch must be"},
		{"invalid header", envelope("*** Frobnicate: a"), "'*** Frobnicate: a' is not a valid hunk header"},
		{"line in an add", "*** Begin Patch\n*** Add File: a\nx\n*** End Patch", "'x' is not a valid hunk header"},
		{"line after a delete", "*** Begin Patch\n*** Delete File: a\n+x\n*** End Patch", "not a valid hunk header"},
		{"empty update", envelope("*** Update File: a", "*** Delete File: b"), "hunk for path 'a' is empty"},
		{"empty update at the end", "*** Begin Patch\n*** Update File: a\n*** End Patch", "hunk for path 'a' is empty"},
		{"update with only a move", "*** Begin Patch\n*** Update File: a\n*** Move to: b\n*** End Patch", "is empty"},
		{"text after the end", "*** Begin Patch\n*** End Patch\nx\n*** End Patch", "last line of the patch must be"},
		{"two environment IDs", envelope("*** Environment ID: a", "*** Environment ID: b"), "more than once"},
		{"empty environment ID", "*** Begin Patch\n*** Environment ID:  \n*** End Patch", "cannot be empty"},
		{"environment ID after a hunk", envelope("*** Delete File: a", "*** Environment ID: b"), "not a valid hunk header"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(tc.patch)
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("got %#v, %v; want error containing %q", got, err, tc.err)
			}
		})
	}
}

func TestOperationKindString(t *testing.T) {
	t.Parallel()
	names := []string{AddFile.String(), DeleteFile.String(), UpdateFile.String(), OperationKind(0).String()}
	if strings.Join(names, " ") != "add delete update OperationKind(0)" {
		t.Fatalf("names: %v", names)
	}
}

func TestApplyFiles(t *testing.T) {
	t.Parallel()
	files := map[string]string{"a.txt": "a\n", "b.txt": "b\n"}
	self := []string{"*** Update File: a.txt", "*** Move to: a.txt", "-a", "+z"}
	for _, tc := range []struct {
		name  string
		mode  Mode
		patch string
		want  map[string]string
		err   string
	}{
		{
			"add replaces, delete removes, update moves over a file",
			PreserveLineEndings, "*** Begin Patch\n*** Add File: a.txt\n+new\n*** Delete File: b.txt\n" +
				"*** Update File: a.txt\n*** Move to: b.txt\n@@\n-new\n+moved\n*** End Patch",
			map[string]string{"b.txt": "moved\n"},
			"",
		},
		{"Codex move to itself deletes", PreserveLineEndings, envelope(self...), map[string]string{"b.txt": "b\n"}, ""},
		{"missing update", PreserveLineEndings, envelope("*** Update File: c.txt", "-a"), nil, "Failed to read file to update c.txt"},
		{"missing delete", NormalizeToLf, envelope("*** Delete File: c.txt"), nil, "Failed to delete file c.txt"},
		{"no operations", PreserveLineEndings, "*** Begin Patch\n*** End Patch", nil, "No files were modified."},
		{
			"bad diff after a good operation", PreserveLineEndings,
			envelope("*** Delete File: b.txt", "*** Update File: a.txt", "bad"), nil, "Unexpected line",
		},
		{"invalid UTF-8 patch", PreserveLineEndings, "*** Begin Patch\n*** Add File: c.txt\n+\xff\n*** End Patch", nil, "not valid UTF-8"},
		{"parse error", NormalizeToLf, "*** Begin Patch", nil, "last line of the patch must be"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before := maps.Clone(files)
			got, err := tc.mode.ApplyFiles(files, tc.patch)
			if !maps.Equal(files, before) {
				t.Fatalf("ApplyFiles changed its input: %q", files)
			}
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("got %q, %v; want error containing %q", got, err, tc.err)
				}
				return
			}
			if err != nil || !maps.Equal(got, tc.want) {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
			got["a.txt"] = "changed"
			if !maps.Equal(files, before) {
				t.Fatal("the result shares storage with the input")
			}
		})
	}
}

func TestApplyFilesNilMap(t *testing.T) {
	t.Parallel()
	got, err := PreserveLineEndings.ApplyFiles(nil, "*** Begin Patch\n*** Add File: a\n+x\n*** End Patch")
	if err != nil || !maps.Equal(got, map[string]string{"a": "x\n"}) {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestApplyFilesPartialState(t *testing.T) {
	t.Parallel()
	// Internally, a failure reports the files as Codex would leave them.
	files := map[string]string{"a.txt": "a\n"}
	got, err := PreserveLineEndings.ApplyFiles(files, "*** Begin Patch\n*** Add File: n.txt\n+n\n"+
		"*** Update File: m.txt\n-x\n*** End Patch")
	if err == nil || !maps.Equal(got, map[string]string{"a.txt": "a\n", "n.txt": "n\n"}) {
		t.Fatalf("got %q, %v", got, err)
	}
}

// Operation.Diff promises that Mode.Apply applies it as ApplyFiles would.
func TestOperationDiffAppliesAsInApplyFiles(t *testing.T) {
	t.Parallel()
	generator := newGenerator(3)
	for range 500 {
		input := generator.text()
		patch := "*** Begin Patch\n*** Update File: f\n" + generator.diff(input) + "*** End Patch"
		parsed, err := Parse(patch)
		if err != nil {
			continue
		}
		for _, mode := range []Mode{NormalizeToLf, PreserveLineEndings} {
			files, filesErr := mode.ApplyFiles(map[string]string{"f": input}, patch)
			applied, applyErr := mode.Apply(input, parsed.Operations[0].Diff)
			if (filesErr == nil) != (applyErr == nil) || (applyErr == nil && files["f"] != applied) {
				t.Fatalf("%v: ApplyFiles gave %q, %v; Apply gave %q, %v\ninput %q\npatch %q",
					mode, files["f"], filesErr, applied, applyErr, input, patch)
			}
		}
	}
}
