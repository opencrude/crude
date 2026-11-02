package catalog

import (
	"bytes"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// maxShebangBytes bounds the first line that detection reads.
const maxShebangBytes = 256

// LanguageForPath identifies the catalog language of a file. head is an
// optional prefix of the file content; nil is valid. The order is: exact file
// names and file name patterns, then the longest catalog extension, then the
// interpreter in a "#!" first line. When several languages claim the same
// file, the result is the first by catalog ID, so the answer is
// deterministic. Linguist data is used when the catalog is generated, not at
// run time: its classifier costs about 10 MB of binary size and 20 ms of
// process start-up.
func (c *Catalog) LanguageForPath(filePath string, head []byte) (Language, bool) {
	id, ok := c.languageIDForPath(filePath, head)
	if !ok {
		return Language{}, false
	}
	return c.Language(id)
}

func (c *Catalog) languageIDForPath(filePath string, head []byte) (string, bool) {
	base := filepath.Base(filePath)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", false
	}
	candidates := c.filenameCandidates(base)
	if len(candidates) == 0 {
		candidates = c.extensionCandidates(base)
	}
	if len(candidates) == 0 {
		if interpreter := shebangInterpreter(head); interpreter != "" {
			candidates = slices.Clone(c.byInterpreter[interpreter])
			if len(candidates) == 0 {
				candidates = slices.Clone(c.byInterpreter[strings.TrimRight(interpreter, "0123456789.")])
			}
		}
	}
	if len(candidates) == 0 {
		return "", false
	}
	return candidates[0], true
}

func (c *Catalog) filenameCandidates(base string) []string {
	out := slices.Clone(c.exactNames[base])
	for _, rule := range c.filenames {
		if !strings.HasPrefix(base, rule.prefix) || !strings.HasSuffix(base, rule.suffix) {
			continue
		}
		if matched, _ := filepath.Match(rule.pattern, base); matched && !slices.Contains(out, rule.language) {
			out = append(out, rule.language)
		}
	}
	slices.Sort(out)
	return out
}

// extensionCandidates tries the longest dotted suffix first, so ".d.ts"-style
// extensions win over their last component.
func (c *Catalog) extensionCandidates(base string) []string {
	lower := strings.ToLower(base)
	dots := make([]int, 0, 4)
	for i := len(lower) - 1; i > 0; i-- {
		if lower[i] == '.' {
			dots = append(dots, i)
			if len(dots) == c.maxExtParts {
				break
			}
		}
	}
	for i := len(dots) - 1; i >= 0; i-- {
		if ids := c.byExtension[lower[dots[i]:]]; len(ids) > 0 {
			return slices.Clone(ids)
		}
	}
	return nil
}

// shebangInterpreter returns the interpreter name of a "#!" first line:
// the base name of the program, or for env the first operand that is not an
// option or an assignment.
func shebangInterpreter(head []byte) string {
	if !bytes.HasPrefix(head, []byte("#!")) {
		return ""
	}
	line := head[2:]
	if len(line) > maxShebangBytes {
		line = line[:maxShebangBytes]
	}
	if end := bytes.IndexAny(line, "\r\n"); end >= 0 {
		line = line[:end]
	}
	fields := strings.Fields(string(line))
	if len(fields) == 0 {
		return ""
	}
	interpreter := path.Base(fields[0])
	if interpreter == "env" {
		interpreter = ""
		for _, field := range fields[1:] {
			if strings.HasPrefix(field, "-") || strings.Contains(field, "=") {
				continue
			}
			interpreter = path.Base(field)
			break
		}
	}
	return interpreter
}
