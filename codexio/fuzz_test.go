// Adapted for Crude from fgn/applypatch 95ed66f6d36a575b00001f2e27aedc1ec7455812.
// See NOTICE for provenance and modification details.
package codexio

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// The checks below hold for every input. The fuzz targets search for
// counterexamples, and the property tests run them on generated cases in
// every `go test`.

func checkCodex(t *testing.T, input, diff string) {
	t.Helper()
	output, err := PreserveLineEndings.Apply(input, diff)
	if err != nil {
		if output != "" {
			t.Fatalf("error %v with output %q", err, output)
		}
		return
	}
	if output != "" && !strings.HasSuffix(output, "\n") && !strings.HasSuffix(output, "\r") {
		t.Fatalf("last line of %q has no ending", output)
	}
	if !strings.ContainsRune(input+diff, '\r') && strings.ContainsRune(output, '\r') {
		t.Fatalf("carriage return in %q from LF input", output)
	}
	if onlyCRLF(input) && !strings.ContainsRune(diff, '\r') && output != "" && !onlyCRLF(output) {
		t.Fatalf("CRLF input %q gave %q", input, output)
	}
	if contextOnly(diff) && output != withFinalEnding(input) {
		t.Fatalf("context-only diff changed %q to %q", input, output)
	}
}

func checkFiles(t *testing.T, files map[string]string, patch string) {
	t.Helper()
	before := maps.Clone(files)
	for _, mode := range []Mode{NormalizeToLf, PreserveLineEndings} {
		output, err := mode.ApplyFiles(files, patch)
		if !maps.Equal(files, before) {
			t.Fatalf("%v changed its input to %q", mode, files)
		}
		if err != nil {
			continue
		}
		parsed, err := Parse(patch)
		if err != nil {
			t.Fatalf("%v applied a patch that does not parse: %v", mode, err)
		}
		named := map[string]bool{}
		for _, operation := range parsed.Operations {
			named[operation.Path], named[operation.MoveTo] = true, true
		}
		for _, name := range append(slicesOfKeys(files), slicesOfKeys(output)...) {
			before, had := files[name]
			after, has := output[name]
			if (had != has || before != after) && !named[name] {
				t.Fatalf("%v changed %s, which the patch does not name", mode, name)
			}
		}
	}
}

func slicesOfKeys(m map[string]string) []string {
	return slices.Collect(maps.Keys(m))
}

// contextOnly reports whether a diff has no "+" or "-" lines.
func contextOnly(diff string) bool {
	for line := range strings.SplitSeq(diff, "\n") {
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
			return false
		}
	}
	return true
}

// onlyCRLF reports whether s has line breaks and all of them are CRLF.
func onlyCRLF(s string) bool {
	rest := strings.ReplaceAll(s, "\r\n", "")
	return len(rest) < len(s) && !strings.ContainsAny(rest, "\r\n")
}

// withFinalEnding gives the last line of s its file's first line ending.
func withFinalEnding(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") || strings.HasSuffix(s, "\r") {
		return s
	}
	_, preferred := splitSource(s)
	return s + preferred
}

func seedDiffs(f *testing.F) {
	f.Helper()
	for _, c := range upstreamCodexCases {
		f.Add(c.input, c.diff)
	}
	generator := newGenerator(11)
	for range 30 {
		text := generator.text()
		f.Add(text, generator.diff(text))
	}
}

func FuzzCodex(f *testing.F) {
	seedDiffs(f)
	f.Fuzz(checkCodex)
}

// FuzzApplyFiles gives every path the seeds use the same contents.
func FuzzApplyFiles(f *testing.F) {
	generator := newGenerator(12)
	for range 30 {
		c := generator.patch("seed")
		f.Add(c.Files["a.txt"], c.Patch)
	}
	for _, c := range regressionCases() {
		f.Add(c.Files["f.txt"], c.Patch)
	}
	f.Fuzz(func(t *testing.T, contents, patch string) {
		files := map[string]string{}
		for _, name := range append(genNames, "f.txt") {
			files[name] = contents
		}
		checkFiles(t, files, patch)
	})
}

func TestInvariantsOnGeneratedCases(t *testing.T) {
	t.Parallel()
	generator := newGenerator(13)
	for range 3000 {
		text := generator.text()
		diff := generator.diff(text)
		checkCodex(t, text, diff)
		c := generator.patch("property")
		checkFiles(t, c.Files, c.Patch)
	}
}
