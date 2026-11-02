// Command namesgate fails when product Go code names a language, tool or
// server that the code-intelligence catalog owns. Knowledge about languages,
// servers and tools lives in data (insight/catalog). Product code asks the
// catalog; it never spells those names itself.
//
// The gate scans string literals in non-test Go files. A literal fails when it
// exactly equals a catalog file extension, a file name or root marker that
// contains a dot, a server ID, a server command, a package name or a package
// binary of four or more letters. Language IDs, language names, bare-word file
// names, short program names and script interpreters are not matched: many are
// ordinary words ("json", "make", "C", "nil", "sh") that product code uses for
// other meanings.
//
// allow.txt lists reviewed exceptions, one per line: "path<TAB>literal<TAB>reason".
// An exception covers one literal in one file. Unused exceptions fail the gate,
// so the list cannot keep stale entries.
//
//	go run ./internal/namesgate
package main

import (
	"bufio"
	_ "embed"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"crude/insight/catalog"
)

//go:embed allow.txt
var allowData string

// skipped holds trees that are not product code: the catalog itself, its
// development tools, vendored third-party code and this gate.
var skipped = []string{
	"insight/catalog",
	"internal/catalogimport",
	"internal/namesgate",
	// Development generator for the wire protocol; it reads Crude's own Go
	// sources, not user projects.
	"internal/wiregen",
	"third_party",
	"site",
	"docs",
	".git",
	".claude",
	"dist",
}

// notLanguageKnowledge lists catalog strings that name something Crude owns
// for another reason. Each needs a reason.
var notLanguageKnowledge = map[string]bool{
	// Version control: some languages use the repository root as a project
	// root, but Crude's Git integration owns this name.
	".git": true,
}

type finding struct {
	path    string
	line    int
	literal string
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	findings, unused, err := run(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "namesgate:", err)
		os.Exit(2)
	}
	for _, f := range findings {
		fmt.Printf("%s:%d: names %q, which the code-intelligence catalog owns; read it from insight/catalog\n", f.path, f.line, f.literal)
	}
	for _, key := range unused {
		fmt.Printf("allow.txt: unused exception %q; remove it\n", key)
	}
	if len(findings) > 0 || len(unused) > 0 {
		fmt.Printf("== NAMES GATE FAILED: %d findings, %d unused exceptions ==\n", len(findings), len(unused))
		os.Exit(1)
	}
	fmt.Println("== NAMES GATE PASSED ==")
}

func run(root string) ([]finding, []string, error) {
	names, err := gatedNames()
	if err != nil {
		return nil, nil, err
	}
	allow, err := parseAllow(allowData)
	if err != nil {
		return nil, nil, err
	}
	used := map[string]bool{}
	var findings []finding
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			for _, skip := range skipped {
				if rel == skip {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil || !names[value] {
				return true
			}
			key := rel + "\t" + value
			if _, ok := allow[key]; ok {
				used[key] = true
				return true
			}
			findings = append(findings, finding{path: rel, line: fset.Position(lit.Pos()).Line, literal: value})
			return true
		})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	var unused []string
	for key := range allow {
		if !used[key] {
			unused = append(unused, key)
		}
	}
	sort.Strings(unused)
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].path != findings[j].path {
			return findings[i].path < findings[j].path
		}
		return findings[i].line < findings[j].line
	})
	return findings, unused, nil
}

// gatedNames is the set of catalog strings that identify a language's files,
// a tool or a server without ambiguity.
func gatedNames() (map[string]bool, error) {
	c, err := catalog.Builtin()
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, language := range c.Languages() {
		for _, list := range [][]string{language.Extensions, language.Filenames, language.Roots, language.WorkspaceRoots} {
			for _, v := range list {
				// A bare word file name (for example "C" or "WORKSPACE") is
				// too ambiguous to identify a language in product code.
				if strings.Contains(v, ".") && !notLanguageKnowledge[v] {
					names[v] = true
				}
			}
		}
		for _, id := range language.Servers {
			server, _ := c.Server(id)
			candidates := []string{server.ID, server.Command}
			if pkg, ok := c.Package(server.Package); ok {
				candidates = append(candidates, pkg.Name)
				for bin := range pkg.Bin {
					candidates = append(candidates, bin)
				}
			}
			for _, name := range candidates {
				// Names of three letters or fewer ("R", "nu", "nil") are
				// ordinary words in product code. The language's file
				// types still identify it.
				if len(name) > 3 {
					names[name] = true
				}
			}
		}
	}
	return names, nil
}

func parseAllow(data string) (map[string]string, error) {
	allow := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(data))
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		parts := strings.SplitN(scanner.Text(), "\t", 3)
		if len(parts) != 3 || strings.TrimSpace(parts[2]) == "" {
			return nil, fmt.Errorf("allow.txt:%d: want path<TAB>literal<TAB>reason", line)
		}
		key := parts[0] + "\t" + parts[1]
		if _, dup := allow[key]; dup {
			return nil, fmt.Errorf("allow.txt:%d: duplicate exception", line)
		}
		allow[key] = parts[2]
	}
	return allow, scanner.Err()
}
