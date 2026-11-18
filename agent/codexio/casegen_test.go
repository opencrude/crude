// Adapted for Crude from fgn/applypatch 95ed66f6d36a575b00001f2e27aedc1ec7455812.
// See NOTICE for provenance and modification details.
package codexio

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

// generator builds seeded random apply_patch cases for the Codex parity
// tests and the property tests. The same seed gives the same cases.
type generator struct {
	r *rand.Rand
}

func newGenerator(seed uint64) *generator {
	return &generator{r: rand.New(rand.NewPCG(seed, 0x6170706c79))}
}

// genCase is a set of files and a patch to apply to them.
type genCase struct {
	Name  string            `json:"name"`
	Files map[string]string `json:"files"`
	Patch string            `json:"patch"`
}

func (c genCase) String() string {
	return fmt.Sprintf("%s\nfiles: %q\npatch: %q", c.Name, c.Files, c.Patch)
}

// genWords are file lines chosen to exercise every matching fallback. The
// same token appears with different whitespace and punctuation so that an
// earlier lenient match competes with a later exact one.
var genWords = []string{
	"alpha", "beta", "gamma", "same", "same",
	"dup", "dup  ", "  dup", "\tdup ", "k - 1", "k – 1", "k - 1",
	"func main() {", "}", "\treturn nil", "'quoted'", "‘quoted’",
	"\"double\"", "“double”", "naïve café ✅", "",
	"@@ at", "*** End of File", "+plus", "-minus", " space",
}

// genNames are the only paths the generator uses: safe relative names with
// no aliases and no file that is also a directory.
var genNames = []string{"a.txt", "b.txt", "dir/c.txt", "d.md"}

func (g *generator) chance(percent int) bool {
	return g.r.IntN(100) < percent
}

func (g *generator) pick(options []string) string {
	return options[g.r.IntN(len(options))]
}

// text returns file contents with a random mix of line endings.
func (g *generator) text() string {
	var endings []string
	switch n := g.r.IntN(100); {
	case n < 60:
		endings = []string{"\n"}
	case n < 75:
		endings = []string{"\r\n"}
	case n < 80:
		endings = []string{"\r"}
	default:
		endings = []string{"\n", "\r\n", "\r"}
	}
	var text strings.Builder
	count := g.r.IntN(9)
	for i := range count {
		text.WriteString(g.pick(genWords))
		if i < count-1 || g.chance(75) {
			text.WriteString(g.pick(endings))
		}
	}
	return text.String()
}

// anyLines splits text on any line ending, dropping a final empty line.
func anyLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// variant returns line, usually unchanged and otherwise with whitespace,
// punctuation, or a typo that a lenient matcher may or may not forgive.
func (g *generator) variant(line string) string {
	switch g.r.IntN(20) {
	case 0:
		return line + g.pick([]string{" ", "  ", "\t"})
	case 1:
		return g.pick([]string{" ", "\t"}) + line
	case 2:
		return strings.NewReplacer("–", "-", "‘", "'", "’", "'",
			"“", "\"", "”", "\"", " ", " ").Replace(line)
	case 3:
		return strings.NewReplacer("-", "–", "'", "’", " ", " ").Replace(line)
	case 4:
		return line + "x"
	}
	return line
}

// diff returns an update diff against text. Most diffs are built from real
// lines of text, in order, and apply; perturbations make many of them fail
// or apply only with a lenient matcher.
func (g *generator) diff(text string) string {
	lines := anyLines(text)
	var diff []string
	position := 0
	for section := range 1 + g.r.IntN(3) {
		switch n := g.r.IntN(100); {
		case n < 20 && section == 0:
		case n < 60:
			diff = append(diff, "@@")
		case n < 90 && len(lines) > 0:
			diff = append(diff, "@@ "+g.variant(g.pick(lines)))
		default:
			diff = append(diff, g.pick([]string{"@@ missing", "@@  ", "@@x", "@@ " + g.pick(genWords)}))
		}
		before := len(diff)
		// Sections usually move forward through the file, as they must.
		start := position + g.r.IntN(len(lines)-position+1)
		if g.chance(10) {
			start = g.r.IntN(len(lines) + 1)
		}
		end := min(len(lines), start+1+g.r.IntN(4))
		position = end
		for _, line := range lines[start:end] {
			if g.chance(25) {
				diff = append(diff, "+"+g.pick(genWords))
			}
			line = g.variant(line)
			switch n := g.r.IntN(100); {
			case n < 55:
				diff = append(diff, " "+line)
			case n < 90:
				diff = append(diff, "-"+line)
			case n < 95:
				diff = append(diff, "")
			default:
				diff = append(diff, " "+g.pick(genWords))
			}
		}
		for g.chance(35) || (len(diff) == before && g.chance(90)) {
			diff = append(diff, "+"+g.pick(genWords))
		}
		if (end == len(lines) && g.chance(40)) || g.chance(5) {
			diff = append(diff, "*** End of File")
		}
		if g.chance(3) {
			diff = append(diff, g.pick([]string{"bad line", "***", "*** Move to: z.txt", "   ", "@@"}))
		}
	}
	return strings.Join(diff, "\n") + "\n"
}

// patch returns files and a patch of one to three operations on them.
func (g *generator) patch(name string) genCase {
	files := map[string]string{}
	for _, file := range genNames {
		if g.chance(85) {
			files[file] = g.text()
		}
	}
	unused := g.r.Perm(len(genNames))
	var patch strings.Builder
	patch.WriteString("*** Begin Patch\n")
	if g.chance(5) {
		patch.WriteString(g.pick([]string{
			"*** Environment ID: env-1\n", "*** Environment ID:\n",
			"*** Environment ID: a\n*** Environment ID: b\n",
		}))
	}
	operations := 1
	if g.chance(40) {
		operations += 1 + g.r.IntN(2)
	}
	for i := range operations {
		// Operations usually target different files, so that a failure is
		// more often a matching failure than a missing file.
		path := genNames[unused[i]]
		if g.chance(15) {
			path = g.pick(genNames)
		}
		switch n := g.r.IntN(100); {
		case n < 3:
			patch.WriteString(g.pick([]string{"*** Frobnicate File: " + path, "*** Add File:", "bad"}) + "\n")
		case n < 15:
			patch.WriteString("*** Add File: " + path + "\n")
			for range g.r.IntN(4) {
				patch.WriteString("+" + g.pick(genWords) + "\n")
			}
			if g.chance(5) {
				patch.WriteString(g.pick([]string{" not added", "", "bad"}) + "\n")
			}
		case n < 25:
			patch.WriteString("*** Delete File: " + path + "\n")
		default:
			patch.WriteString("*** Update File: " + path + "\n")
			if g.chance(3) {
				// Codex ignores "*** End of File" before the first section.
				patch.WriteString("*** End of File\n")
			}
			if g.chance(15) {
				patch.WriteString("*** Move to: " + g.pick(append([]string{"sub/e.txt", path}, genNames...)) + "\n")
			}
			patch.WriteString(g.diff(files[path]))
		}
	}
	patch.WriteString("*** End Patch")
	if g.chance(50) {
		patch.WriteString("\n")
	}
	return genCase{Name: name, Files: files, Patch: g.envelope(patch.String())}
}

// envelope varies how the patch is wrapped and spaced.
func (g *generator) envelope(patch string) string {
	switch n := g.r.IntN(100); {
	case n < 8:
		return "<<'EOF'\n" + patch + "\nEOF\n"
	case n < 16:
		return strings.ReplaceAll(patch, "\n", "\r\n")
	case n < 21:
		return " " + strings.Replace(patch, "*** End Patch", "*** End Patch  ", 1) + "\n\n"
	case n < 24:
		return strings.Replace(patch, "\n*** ", "\n  *** ", 1)
	case n < 26:
		return strings.TrimPrefix(patch, "*** Begin Patch\n")
	case n < 28:
		return strings.Replace(patch, "*** End Patch", "*** End Patch\n\n*** End Patch", 1)
	}
	return patch
}
