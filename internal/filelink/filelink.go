// Package filelink recognizes source-file references without filesystem access.
// Paths keep the core host's spelling; resolution belongs to the document owner.
package filelink

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"crude/insight/catalog"
)

// Reference has a path and optional one-based source coordinates.
type Reference struct {
	Path         string
	Line, Column int
}

// Parse accepts a local Markdown destination, never a URL or executable scheme.
// Both native path syntaxes are understood regardless of the shell host.
func Parse(target string) (Reference, bool) {
	if target == "" || len(target) > 8192 || strings.HasPrefix(target, "//") || strings.HasPrefix(target, `\\`) {
		return Reference{}, false
	}
	value := target
	// A drive letter is part of a path, not a URL scheme.
	drive := drivePath(value)
	var ref Reference
	if path, fragment, ok := strings.Cut(value, "#"); ok {
		value = path
		if strings.HasPrefix(fragment, "L") {
			location, _, _ := strings.Cut(fragment[1:], "-")
			line, column, _ := strings.Cut(location, "C")
			ref.Line, _ = positive(line)
			ref.Column, _ = positive(column)
		}
	}
	if head, n, ok := cutNumber(value); ok {
		value, ref.Line = head, n
		if head, n, ok := cutNumber(value); ok {
			value, ref.Line, ref.Column = head, n, ref.Line
		}
	}
	path, err := url.PathUnescape(value)
	if err != nil || path == "" || strings.TrimSpace(path) != path {
		return Reference{}, false
	}
	for i, r := range path {
		if unicode.IsControl(r) || strings.ContainsRune("*?<>|\"`", r) || r == ':' && (!drive || i != 1) ||
			r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069' {
			return Reference{}, false
		}
	}
	if strings.HasPrefix(path, "//") || strings.HasPrefix(path, `\\`) {
		return Reference{}, false
	}
	ref.Path = path
	return ref, true
}

// Candidate is deliberately stricter than an authored Markdown destination.
// Slash paths and dotted filenames qualify; words, versions and globs do not.
func Candidate(value string) bool {
	if !strings.ContainsAny(value, `/\.`) {
		return false
	}
	ref, ok := Parse(value)
	if !ok {
		return false
	}
	base := ref.Path
	slash := strings.LastIndexAny(base, `/\`)
	if slash >= 0 {
		first := strings.IndexAny(base, `/\`)
		if strings.ContainsAny(base[:first], " \t=") {
			return false // a command such as "go test ./src/main.go"
		}
		base = base[slash+1:]
	} else if strings.ContainsRune(base, '@') {
		return false // an email address, not an inferred filename
	}
	if base == "" || base == "." || base == ".." {
		return false
	}
	if dot := strings.LastIndexByte(base, '.'); dot >= 0 && dot < len(base)-1 {
		letter := false
		for _, r := range base[dot+1:] {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
				return false
			}
			letter = letter || unicode.IsLetter(r)
		}
		if !letter {
			return false
		}
		if slash >= 0 {
			return true
		}
		// Without a directory, a dotted word is usually code or prose
		// ("tools[10].parameters", "fmt.Println", "i.e."). Link it only
		// when the language catalog knows the name or its extension.
		// Common prose abbreviations stay text; single-letter source names
		// such as x.go and x.c must still work.
		if strings.EqualFold(base, "i.e") || strings.EqualFold(base, "e.g") {
			return false
		}
		_, known := catalog.Current().LanguageForPath(base, nil)
		return known
	}
	// Extensionless explicit paths and conventional capitalized filenames are
	// useful, but slash-separated prose ("and/or") is not a file reference.
	return slash >= 0 && !strings.ContainsAny(ref.Path, " \t+") &&
		(strings.HasPrefix(ref.Path, ".") || strings.HasPrefix(ref.Path, "/") || drivePath(ref.Path) ||
			base[0] >= 'A' && base[0] <= 'Z')
}

func drivePath(value string) bool {
	return len(value) >= 3 && asciiLetter(value[0]) && value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}

func cutNumber(value string) (string, int, bool) {
	i := strings.LastIndexByte(value, ':')
	if i < 0 {
		return value, 0, false
	}
	n, ok := positive(value[i+1:])
	return value[:i], n, ok
}

func positive(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(value)
	return n, err == nil && n > 0
}

func asciiLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
