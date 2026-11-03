// Command namesgate fails when product Go code names a language, tool or
// server that the code-intelligence catalog owns. Knowledge about languages,
// servers and tools lives in data (insight/catalog). Product code asks the
// catalog; it never spells those names itself.
//
// Distinctive strings are checked everywhere. Ambiguous catalog names are
// checked in comparisons and switch cases whose operand names indicate language,
// grammar, server, tool, command, file, extension or interpreter context.
// Identifier operands in those branches are checked case-insensitively. This is
// a heuristic, not a semantic proof: aliases, indirect values and opaque names
// can hide knowledge, while unrelated code can share the same vocabulary.
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
	allNames := map[string]bool{}
	c, err := catalog.Builtin()
	if err != nil {
		return nil, nil, err
	}
	for _, name := range c.Names() {
		if name != "" && !notLanguageKnowledge[name] {
			allNames[strings.ToLower(name)] = true
		}
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
		// A function whose signature carries a language-related type can use
		// a short local ID as its switch tag.
		contextualIDs := map[*ast.SwitchStmt]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !relevantContext(fn.Type) {
				return true
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sw, ok := n.(*ast.SwitchStmt)
				if ok {
					if id, ok := sw.Tag.(*ast.Ident); ok && strings.EqualFold(id.Name, "id") {
						contextualIDs[sw] = true
					}
				}
				return true
			})
			return false
		})
		reported := map[token.Pos]bool{}
		report := func(node ast.Node, value string) {
			if reported[node.Pos()] {
				return
			}
			reported[node.Pos()] = true
			key := rel + "\t" + value
			if _, ok := allow[key]; ok {
				used[key] = true
				return
			}
			findings = append(findings, finding{path: rel, line: fset.Position(node.Pos()).Line, literal: value})
		}
		checkBranch := func(expr ast.Expr) {
			ast.Inspect(expr, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.BasicLit:
					if n.Kind == token.STRING {
						value, err := strconv.Unquote(n.Value)
						if err == nil && allNames[strings.ToLower(value)] {
							report(n, value)
						}
					}
				case *ast.Ident:
					// Single-letter identifiers and predeclared values are too
					// common to attribute to a language without type analysis.
					if len(n.Name) > 1 && n.Name != "nil" && n.Name != "true" && n.Name != "false" && allNames[strings.ToLower(n.Name)] {
						report(n, n.Name)
					}
				}
				return true
			})
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BasicLit:
				if n.Kind == token.STRING {
					value, err := strconv.Unquote(n.Value)
					if err == nil && names[value] {
						report(n, value)
					}
				}
			case *ast.BinaryExpr:
				if n.Op == token.EQL || n.Op == token.NEQ {
					if relevantContext(n.X) {
						checkBranch(n.Y)
					}
					if relevantContext(n.Y) {
						checkBranch(n.X)
					}
				}
			case *ast.SwitchStmt:
				if n.Tag != nil && (relevantContext(n.Tag) || contextualIDs[n]) {
					for _, stmt := range n.Body.List {
						clause := stmt.(*ast.CaseClause)
						for _, expr := range clause.List {
							checkBranch(expr)
						}
					}
				}
			}
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

// relevantContext restricts ambiguous names to operands with a meaningful
// code-intelligence role. It deliberately does not match arbitrary substrings.
func relevantContext(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		name := strings.ToLower(id.Name)
		for _, part := range []string{"language", "grammar", "server", "tool", "command", "filename", "filepath", "extension", "interpreter", "langid"} {
			if strings.Contains(name, part) {
				found = true
				break
			}
		}
		return true
	})
	return found
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
