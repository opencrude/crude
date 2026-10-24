package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/mod/modfile"
)

const cohortOutput = "buildinfo/cohort_gen.go"

// cohortDigest identifies source, not compiler output or the current host. Every
// platform/tag alternative is included. No Git, package loading, native library
// output, dependency cache, or executable is consulted. The supported native
// library inputs are their checked-in pins, not target-specific archive bytes.
func cohortDigest(root string) (string, error) {
	files, err := cohortFiles(root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = h.Write([]byte("crude-source-cohort-v1\x00"))
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return "", err
		}
		// Frame both path and contents, so file boundaries cannot collide.
		for _, field := range [][]byte{[]byte(name), data} {
			var size [8]byte
			binary.BigEndian.PutUint64(size[:], uint64(len(field)))
			_, _ = h.Write(size[:])
			_, _ = h.Write(field)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func cohortFiles(root string) ([]string, error) {
	manifest, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	mod, err := modfile.Parse("go.mod", manifest, nil)
	if err != nil {
		return nil, err
	}
	// Local replacements must be inside the checkout to have portable canonical
	// names. Reject external/symlinked sources rather than advertise false equality.
	roots := []string{"."}
	for _, replace := range mod.Replace {
		if replace.New.Version != "" {
			continue
		}
		name := path.Clean(filepath.ToSlash(replace.New.Path))
		if name == "." || !fs.ValidPath(name) || filepath.IsAbs(replace.New.Path) {
			return nil, fmt.Errorf("cohort: local replacement must be checkout-relative: %s", replace.New.Path)
		}
		roots = append(roots, name)
	}
	// Pathwalk is vendored in the crude module rather than a module replacement.
	if _, err := os.Stat(filepath.Join(root, "third_party/pathwalk")); err == nil {
		roots = append(roots, "third_party/pathwalk")
	}
	files := map[string]bool{}
	add := func(name string) error {
		if name == cohortOutput {
			return nil
		}
		// Lstat each component: neither directories nor files may escape via links.
		for current := name; current != "."; current = path.Dir(current) {
			info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(current)))
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("cohort: symlink input %s", current)
			}
			if current == name && !info.Mode().IsRegular() {
				return fmt.Errorf("cohort: non-regular input %s", name)
			}
		}
		files[name] = true
		return nil
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		if err := add(name); err != nil {
			return nil, err
		}
	}
	for _, name := range []string{"deps/GHOSTTY_PIN", "deps/GHOSTTY_PIN.ps1"} {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name))); os.IsNotExist(err) {
			continue // Tiny fixture/module roots need not use the native dependency.
		}
		if err := add(name); err != nil {
			return nil, err
		}
	}
	for _, sourceRoot := range roots {
		for current := sourceRoot; current != "."; current = path.Dir(current) {
			info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(current)))
			if err != nil {
				return nil, err
			}
			if !info.IsDir() {
				return nil, fmt.Errorf("cohort: source root must be a real directory: %s", current)
			}
		}
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(sourceRoot)), func(filename string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(root, filename)
			if err != nil {
				return err
			}
			name := filepath.ToSlash(rel)
			if entry.IsDir() {
				if name != sourceRoot && skipCohortDir(entry.Name()) {
					return filepath.SkipDir
				}
				// Nested modules are only included through explicit replacements.
				if name != sourceRoot {
					if _, err := os.Stat(filepath.Join(filename, "go.mod")); err == nil {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if !cohortSource(entry.Name()) && !promptTemplate(name) || name == cohortOutput {
				return nil
			}
			if err := add(name); err != nil {
				return err
			}
			if strings.HasSuffix(name, ".go") {
				return addEmbeds(root, name, add)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func promptTemplate(name string) bool {
	if !strings.HasPrefix(name, "prompt/") {
		return false
	}
	switch path.Ext(name) {
	case ".tmpl", ".gotmpl", ".tpl", ".txt", ".md":
		return true
	}
	return false
}

func skipCohortDir(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return true
	}
	switch name {
	case "third_party", "deps", "vendor", "node_modules", "testdata", "tests", "test", "dist", "build", "artifacts", "tmp", "temp", "cache":
		return true
	}
	return false
}

func cohortSource(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || strings.HasSuffix(name, "_test.go") {
		return false
	}
	if name == "go.mod" || name == "go.sum" {
		return true
	}
	switch filepath.Ext(name) {
	case ".go", ".c", ".h", ".s", ".S", ".cc", ".cpp", ".cxx", ".m", ".mm", ".f", ".f90", ".swig", ".swigcxx":
		return true
	}
	return false
}

// Inspect directives in every Go source, not just the host-selected package.
// Parsing comments avoids mistaking a directive example inside a string for an
// actual embed. Embedded directory expansion follows Go's hidden-file policy.
func addEmbeds(root, source string, add func(string) error) error {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(source)))
	if err != nil || !bytes.Contains(data, []byte("//go:embed")) {
		return err
	}
	f, err := parser.ParseFile(token.NewFileSet(), source, data, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	base := path.Dir(source)
	for _, group := range f.Comments {
		for _, comment := range group.List {
			text, ok := strings.CutPrefix(comment.Text, "//go:embed")
			if !ok || len(text) == 0 || (text[0] != ' ' && text[0] != '\t') {
				continue
			}
			patterns, err := embedPatterns(text)
			if err != nil {
				return fmt.Errorf("%s: %w", source, err)
			}
			for _, pattern := range patterns {
				all := strings.HasPrefix(pattern, "all:")
				pattern = strings.TrimPrefix(pattern, "all:")
				if !fs.ValidPath(pattern) {
					return fmt.Errorf("%s: invalid embed pattern %q", source, pattern)
				}
				matches, err := fs.Glob(os.DirFS(filepath.Join(root, filepath.FromSlash(base))), pattern)
				if err != nil {
					return err
				}
				if len(matches) == 0 {
					return fmt.Errorf("%s: embed pattern %q matches no files", source, pattern)
				}
				for _, match := range matches {
					name := path.Join(base, match)
					err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(name)), func(filename string, entry fs.DirEntry, walkErr error) error {
						if walkErr != nil {
							return walkErr
						}
						if !all && filename != filepath.Join(root, filepath.FromSlash(name)) && (strings.HasPrefix(entry.Name(), ".") || strings.HasPrefix(entry.Name(), "_")) {
							if entry.IsDir() {
								return filepath.SkipDir
							}
							return nil
						}
						if entry.IsDir() {
							return nil
						}
						rel, err := filepath.Rel(root, filename)
						if err != nil {
							return err
						}
						return add(filepath.ToSlash(rel))
					})
					if err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func embedPatterns(text string) ([]string, error) {
	var patterns []string
	for text = strings.TrimSpace(text); text != ""; text = strings.TrimSpace(text) {
		var pattern string
		if text[0] == '"' || text[0] == '`' {
			quoted, err := strconv.QuotedPrefix(text)
			if err != nil {
				return nil, err
			}
			pattern, err = strconv.Unquote(quoted)
			if err != nil {
				return nil, err
			}
			text = text[len(quoted):]
		} else {
			end := strings.IndexFunc(text, unicode.IsSpace)
			if end < 0 {
				end = len(text)
			}
			pattern, text = text[:end], text[end:]
		}
		patterns = append(patterns, pattern)
	}
	return patterns, nil
}

func cohortSourceFile(digest string) []byte {
	return fmt.Appendf(nil, "// Code generated by wiregen; DO NOT EDIT.\n\npackage buildinfo\n\n// ProtocolBuild identifies the exact platform-independent production source cohort.\n// It is distinct from Schema (wire semantics) and ExecutableID (local image bytes).\n// Supported builds require go run ./internal/wiregen -check; plain go build does\n// not run generators or establish freshness after source edits.\nconst ProtocolBuild = %q\n", digest)
}

func writeCohort(root string) error {
	digest, err := cohortDigest(root)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, filepath.FromSlash(cohortOutput)), cohortSourceFile(digest), 0o644)
}

func checkCohort(root string) error {
	digest, err := cohortDigest(root)
	if err != nil {
		return err
	}
	actual, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(cohortOutput)))
	if err != nil {
		return fmt.Errorf("protocol cohort missing: run go run ./internal/wiregen: %w", err)
	}
	if !bytes.Equal(actual, cohortSourceFile(digest)) {
		return fmt.Errorf("protocol cohort is stale: run go run ./internal/wiregen before building (want %s)", digest)
	}
	return nil
}
