// Command layoutcheck fails when the repository breaks Crude's folder,
// package, file or exported-name rules. Each rule is documented in
// docs/reference/code-layout.md; every error names its rule, the place and the
// fix, so a contributor can act without reading this program.
//
// It reads the tree from git (tracked and new, not ignored, files), package
// facts from "go list" and declarations from go/parser. It needs no cgo and
// does not build anything.
//
// allow.txt lists reviewed exceptions, one per line:
// "RULE<TAB>path<TAB>name<TAB>reason". The name is "-" when the rule is about
// a path. Unused exceptions fail, so the list cannot keep stale entries.
//
//	go run ./tools/layoutcheck
package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

//go:embed allow.txt
var allowData string

const module = "crude"

const reference = "docs/reference/code-layout.md"

// topLevel is the complete list of top-level entries. Anything else at the
// root needs a decision recorded here and in the code layout reference.
var topLevel = map[string]bool{
	// Folders.
	".github": true, ".githooks": true, "agent": true, "app": true, "assets": true,
	"core": true, "docs": true, "insight": true, "internal": true, "lsp": true,
	"scripts": true, "site": true, "text": true, "third_party": true, "tools": true,
	"tui": true, "types": true, "workspace": true,
	// Files.
	".gitattributes": true, ".gitignore": true, ".gitleaksignore": true, ".go-version": true,
	".golangci.yml": true, "AGENTS.md": true, "LICENSE-APACHE": true, "LICENSE-MIT": true,
	"Makefile": true, "NOTICE": true, "README.md": true, "compile_flags.txt": true,
	"go.mod": true, "go.sum": true, "install.ps1": true, "install.sh": true,
	"main.go": true, "pyrightconfig.json": true,
}

// groups are folders that only arrange packages. They hold no Go files.
var groups = []string{"agent", "insight", "internal", "lsp", "text", "tools", "types", "tui/screens"}

// runtimeLayers may not be imported by packages under internal.
var runtimeLayers = []string{"app", "core", "agent", "workspace", "tui"}

// badPackageNames say nothing about what a package provides.
var badPackageNames = map[string]bool{
	"api": true, "base": true, "common": true, "helper": true, "helpers": true,
	"interfaces": true, "misc": true, "model": true, "models": true, "shared": true,
	"types": true, "util": true, "utils": true,
}

// catchAllFiles are file names that collect unrelated code. Tests may use
// them for shared fixtures (helpers_test.go).
var catchAllFiles = map[string]bool{
	"common": true, "foundation": true, "helper": true, "helpers": true,
	"misc": true, "util": true, "utils": true,
}

// platformWords are the GOOS and GOARCH values the go command reads from a
// file name suffix, plus the family words Crude uses for fallback variants.
var (
	goos = words("aix android darwin dragonfly freebsd hurd illumos ios js linux nacl netbsd openbsd plan9 solaris wasip1 windows zos")
	arch = words("386 amd64 amd64p32 arm arm64 arm64be armbe loong64 mips mips64 mips64le mips64p32 mips64p32le mipsle ppc ppc64 ppc64le riscv riscv64 s390 s390x sparc sparc64 wasm")
	// A fallback or shared variant ends in one of these instead of a platform.
	families = words("unix other bsd posix cgo nocgo")
)

var (
	fileNamePattern = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*\.go$`)
	packagePattern  = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
	strayPattern    = regexp.MustCompile(`(\.go\.[0-9]+|~|\.orig|\.rej|\.bak)$`)
	buildLine       = regexp.MustCompile(`(?m)^//go:build (.+)$`)
)

type pkg struct {
	ImportPath     string
	Name           string
	Dir            string
	GoFiles        []string
	CgoFiles       []string
	IgnoredGoFiles []string
	TestGoFiles    []string
	XTestGoFiles   []string
	Imports        []string
}

// rel is the package folder relative to the repository root.
func (p *pkg) rel() string {
	if p.ImportPath == module {
		return "."
	}
	return strings.TrimPrefix(p.ImportPath, module+"/")
}

// sources are the non-test files of every build configuration.
func (p *pkg) sources() []string {
	var out []string
	for _, list := range [][]string{p.GoFiles, p.CgoFiles, p.IgnoredGoFiles} {
		for _, f := range list {
			if !strings.HasSuffix(f, "_test.go") {
				out = append(out, f)
			}
		}
	}
	sort.Strings(out)
	return out
}

type finding struct {
	rule, place, name, message, fix string
}

type checker struct {
	root     string
	files    []string // repository-relative, slash-separated
	pkgs     map[string]*pkg
	allow    map[string]string // rule\tpath\tname -> reason
	used     map[string]bool
	findings []finding
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		fail(err)
	}
	findings, err := run(root, allowData)
	if err != nil {
		fail(err)
	}
	os.Exit(report(os.Stdout, findings))
}

// run checks the repository at root and returns every problem, including
// allow.txt entries that no longer match anything.
func run(root, allow string) ([]finding, error) {
	c := newChecker(root)
	var err error
	if c.allow, err = parseAllow(allow); err != nil {
		return nil, err
	}
	if err := c.load(); err != nil {
		return nil, err
	}
	c.checkTree()
	c.checkPackages()
	c.checkThirdParty()
	c.checkAllowUsed()
	return c.findings, nil
}

func newChecker(root string) *checker {
	return &checker{root: root, pkgs: map[string]*pkg{}, allow: map[string]string{}, used: map[string]bool{}}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "layoutcheck:", err)
	os.Exit(2)
}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(s) {
		out[w] = true
	}
	return out
}

func parseAllow(data string) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 4 || strings.TrimSpace(parts[3]) == "" {
			return nil, fmt.Errorf("allow.txt:%d: want RULE<TAB>path<TAB>name<TAB>reason", n)
		}
		out[parts[0]+"\t"+parts[1]+"\t"+parts[2]] = parts[3]
	}
	return out, sc.Err()
}

func (c *checker) load() error {
	ls := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	ls.Dir = c.root
	out, err := ls.Output()
	if err != nil {
		return fmt.Errorf("git ls-files: %w", err)
	}
	for _, f := range bytes.Split(out, []byte{0}) {
		if len(f) == 0 {
			continue
		}
		name := string(f)
		if _, err := os.Lstat(filepath.Join(c.root, name)); err != nil {
			continue // deleted in the work tree but still in the index
		}
		c.files = append(c.files, name)
	}
	cmd := exec.Command("go", "list", "-e", "-json=ImportPath,Name,Dir,GoFiles,CgoFiles,IgnoredGoFiles,TestGoFiles,XTestGoFiles,Imports", "./...")
	cmd.Dir = c.root
	cmd.Stderr = os.Stderr
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("go list: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var p pkg
		if err := dec.Decode(&p); err != nil {
			return fmt.Errorf("go list: %w", err)
		}
		if strings.HasPrefix(p.rel(), "third_party/") {
			continue
		}
		c.pkgs[p.ImportPath] = &p
	}
	return nil
}

// add records a finding unless allow.txt excuses it.
func (c *checker) add(rule, place, name, message, fix string) {
	file := place
	if i := strings.Index(place, ":"); i >= 0 {
		file = place[:i]
	}
	key := rule + "\t" + file + "\t" + name
	if _, ok := c.allow[key]; ok {
		c.used[key] = true
		return
	}
	c.findings = append(c.findings, finding{rule, place, name, message, fix})
}

func (c *checker) checkAllowUsed() {
	for key := range c.allow {
		if !c.used[key] {
			parts := strings.Split(key, "\t")
			c.findings = append(c.findings, finding{"ALLOW-UNUSED", "tools/layoutcheck/allow.txt", parts[2],
				fmt.Sprintf("the exception for %s at %s no longer matches anything", parts[0], parts[1]),
				"Delete the line."})
		}
	}
}

// report prints the findings with their fix and rule link and returns the
// process exit code.
func report(w io.Writer, findings []finding) int {
	if len(findings) == 0 {
		fmt.Fprintln(w, "layoutcheck: OK")
		return 0
	}
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.place != b.place {
			return a.place < b.place
		}
		return a.rule < b.rule
	})
	for _, f := range findings {
		fmt.Fprintf(w, "%s: %s: %s\n    fix: %s\n    rule: %s#%s\n", f.place, f.rule, f.message, f.fix, reference, strings.ToLower(f.rule))
	}
	fmt.Fprintf(w, "\nlayoutcheck: %d problem(s). A reviewed exception goes in tools/layoutcheck/allow.txt with its reason.\n", len(findings))
	return 1
}

// checkTree applies the rules that need only paths.
func (c *checker) checkTree() {
	seenTop := map[string]bool{}
	for _, f := range c.files {
		top := strings.SplitN(f, "/", 2)[0]
		if !topLevel[top] && !seenTop[top] {
			seenTop[top] = true
			c.add("LAYOUT-TOP", top, "-", fmt.Sprintf("%q is not one of the top-level entries", top),
				"Move it into the folder that owns it (see the target tree), or add it to topLevel in tools/layoutcheck with a reason.")
		}
		if strayPattern.MatchString(f) {
			c.add("FILE-STRAY", f, "-", "this is an editor or tool copy, not a source file", "Delete it.")
		}
		if strings.HasPrefix(f, "third_party/") || strings.Contains(f, "/testdata/") || !strings.HasSuffix(f, ".go") {
			continue
		}
		dir, base := path.Split(f)
		dir = strings.TrimSuffix(dir, "/")
		for _, g := range groups {
			if dir == g {
				c.add("LAYOUT-GROUP", f, "-", fmt.Sprintf("%s/ is a group folder; it holds packages, not Go files", g),
					"Move the file into the child package that owns it.")
			}
		}
		if !fileNamePattern.MatchString(base) {
			c.add("FILE-NAME", f, "-", "file names are lowercase words joined by underscores",
				"Rename it, for example "+suggestFile(base)+".")
		}
		if catchAllFiles[strings.TrimSuffix(base, ".go")] {
			c.add("FILE-NAME", f, "-", base+" says nothing about what the file holds",
				"Move each declaration into the file of its subject or its callers, or name the file after its subject.")
		}
	}
}

func suggestFile(base string) string {
	var b strings.Builder
	prev := rune(0)
	for _, r := range strings.TrimSuffix(base, ".go") {
		switch {
		case r == '-' || r == ' ' || r == '.':
			r = '_'
		case unicode.IsUpper(r):
			if prev != 0 && prev != '_' {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String() + ".go"
}

// checkPackages applies the rules that need package facts or declarations.
func (c *checker) checkPackages() {
	paths := make([]string, 0, len(c.pkgs))
	for p := range c.pkgs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, ip := range paths {
		p := c.pkgs[ip]
		rel := p.rel()
		base := path.Base(rel)
		if rel == "." {
			base = "main"
		}
		c.checkName(p, rel, base)
		c.checkChildren(p, rel)
		c.checkInternal(p, rel)
		c.checkFiles(p, rel, base)
	}
}

func (c *checker) checkName(p *pkg, rel, base string) {
	if p.Name == "main" || p.Name == "" {
		return
	}
	if !packagePattern.MatchString(p.Name) {
		c.add("NAME-PACKAGE", rel, p.Name, fmt.Sprintf("package %s: names use lowercase letters and digits only", p.Name),
			"Join the words without separators, as in tabwriter.")
	}
	if p.Name != base {
		c.add("NAME-FOLDER", rel, p.Name, fmt.Sprintf("package %s lives in folder %q; the package name is the last element of its path", p.Name, base),
			fmt.Sprintf("Rename the folder to %q (or the package to %q).", p.Name, base))
	}
	if badPackageNames[p.Name] {
		c.add("NAME-MEANING", rel, p.Name, fmt.Sprintf("package %s does not say what it provides", p.Name),
			"Split it by subject and name each package after what callers get from it.")
	}
}

// checkChildren enforces that a package folder only holds its parts or its
// extensions: the parent uses the child (directly or through other packages
// of the module), or the child builds on the parent.
func (c *checker) checkChildren(p *pkg, rel string) {
	if rel == "." {
		return
	}
	reach := c.reach(p.ImportPath)
	for _, child := range c.pkgs {
		if path.Dir(child.ImportPath) != p.ImportPath {
			continue
		}
		if reach[child.ImportPath] || c.extends(child, p.ImportPath, reach) {
			continue
		}
		c.add("LAYOUT-CHILD", child.rel(), "-",
			fmt.Sprintf("%s/ holds package %s, but %s neither uses it nor is extended by it", rel, child.rel(), rel),
			"Move the child next to the packages it belongs with, or make the parent folder a group with no Go files.")
	}
}

// extends reports whether child builds on parent: it imports the parent, or
// a sibling package that the parent itself uses (one of the parent's parts,
// such as a protocol package that a client package speaks).
func (c *checker) extends(child *pkg, parent string, reach map[string]bool) bool {
	for _, imp := range child.Imports {
		if imp == parent || (path.Dir(imp) == parent && reach[imp]) {
			return true
		}
	}
	return false
}

func (c *checker) reach(from string) map[string]bool {
	seen := map[string]bool{}
	stack := []string{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		p := c.pkgs[cur]
		if p == nil {
			continue
		}
		for _, imp := range p.Imports {
			if (imp == module || strings.HasPrefix(imp, module+"/")) && !seen[imp] {
				seen[imp] = true
				stack = append(stack, imp)
			}
		}
	}
	return seen
}

func (c *checker) checkInternal(p *pkg, rel string) {
	if !strings.HasPrefix(rel, "internal/") {
		return
	}
	for _, imp := range p.Imports {
		for _, layer := range runtimeLayers {
			if imp == module+"/"+layer || strings.HasPrefix(imp, module+"/"+layer+"/") {
				c.add("LAYOUT-INTERNAL", rel, strings.TrimPrefix(imp, module+"/"),
					fmt.Sprintf("%s imports %s; packages under internal/ are shared support and never import app, core, agent, workspace or tui", rel, strings.TrimPrefix(imp, module+"/")),
					"Move the package under the layer it depends on, or move the needed type down into internal/ or types/.")
			}
		}
	}
}

func (c *checker) checkFiles(p *pkg, rel, base string) {
	fset := token.NewFileSet()
	sources := p.sources()
	var commented []string
	for _, name := range sources {
		file := path.Join(rel, name)
		if rel == "." {
			file = name
		}
		src, err := os.ReadFile(filepath.Join(p.Dir, name))
		if err != nil {
			continue
		}
		f, err := parser.ParseFile(fset, file, src, parser.ParseComments)
		if err != nil {
			continue // the compiler reports syntax errors
		}
		if f.Doc != nil {
			commented = append(commented, name)
		}
		c.checkPlatform(file, name, src)
		if p.Name != "main" {
			c.checkDecls(fset, f, file, p.Name)
		}
	}
	for _, list := range [][]string{p.TestGoFiles, p.XTestGoFiles} {
		for _, name := range list {
			src, err := os.ReadFile(filepath.Join(p.Dir, name))
			if err == nil {
				c.checkPlatform(path.Join(rel, name), name, src)
			}
		}
	}
	entry := base + ".go"
	if p.Name == "main" {
		entry = "main.go"
	}
	switch {
	case len(sources) == 0:
	case len(commented) == 0:
		c.add("DOC-PACKAGE", rel, "-", fmt.Sprintf("package %s has no package comment", p.Name),
			fmt.Sprintf("Add \"// Package %s ...\" (or \"// Command ...\" for a program) directly above the package clause in %s or doc.go.", p.Name, entry))
	case len(commented) > 1:
		c.add("DOC-PACKAGE", rel, "-", fmt.Sprintf("package %s has %d package comments (%s); a package has exactly one", p.Name, len(commented), strings.Join(commented, ", ")),
			"Keep one, in the entry file or doc.go, and delete the others.")
	case len(sources) >= 3 && commented[0] != entry && commented[0] != "doc.go":
		c.add("DOC-ENTRY", path.Join(rel, commented[0]), "-",
			fmt.Sprintf("the package comment of %s is in %s; a package with three or more files keeps it in %s or doc.go", p.Name, commented[0], entry),
			fmt.Sprintf("Move the comment to %s, or to a doc.go that holds only the comment and the package clause.", entry))
	}
}

// checkPlatform enforces that platform variants say so in their name.
func (c *checker) checkPlatform(file, name string, src []byte) {
	stem := strings.TrimSuffix(strings.TrimSuffix(name, ".go"), "_test")
	if goos[stem] || arch[stem] || families[stem] {
		c.add("FILE-PLATFORM", file, "-",
			fmt.Sprintf("%s is named only after a platform; the go command reads a platform from a _suffix only, so this file has no implicit constraint", name),
			"Name the concept first and the platform last, for example backend_"+stem+".go.")
		return
	}
	if strings.HasSuffix(name, "_test.go") {
		return // a test may name its scenario; its build line is explicit
	}
	m := buildLine.FindSubmatch(src)
	if m == nil {
		return
	}
	mentions := false
	for _, w := range regexp.MustCompile(`[a-z0-9]+`).FindAllString(string(m[1]), -1) {
		if goos[w] || arch[w] || w == "unix" {
			mentions = true
		}
	}
	if !mentions {
		return
	}
	parts := strings.Split(stem, "_")
	last := parts[len(parts)-1]
	if len(parts) > 1 && (goos[last] || arch[last] || families[last]) {
		return
	}
	c.add("FILE-PLATFORM", file, "-",
		fmt.Sprintf("%s is built only on some platforms (//go:build %s) but its name does not say which", name, strings.TrimSpace(string(m[1]))),
		"End the name with the platform, or with _other for the fallback, for example "+stem+"_other.go.")
}

// checkDecls enforces the exported-name rules that no linter covers.
func (c *checker) checkDecls(fset *token.FileSet, f *ast.File, file, pkgName string) {
	title := strings.ToUpper(pkgName[:1]) + pkgName[1:]
	repeats := func(name string) bool {
		if !strings.HasPrefix(strings.ToLower(name), pkgName) || len(name) <= len(pkgName) {
			return false
		}
		r, _ := utf8.DecodeRuneInString(name[len(pkgName):])
		return unicode.IsUpper(r) || unicode.IsDigit(r)
	}
	at := func(n ast.Node) string { return fmt.Sprintf("%s:%d", file, fset.Position(n.Pos()).Line) }
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			if d.Recv == nil && repeats(d.Name.Name) {
				c.add("NAME-REPEAT", at(d), d.Name.Name, fmt.Sprintf("%s.%s repeats the package name", pkgName, d.Name.Name),
					fmt.Sprintf("Rename it to %s; callers already write %s.", strings.TrimPrefix(d.Name.Name, title), pkgName))
			}
			if len(d.Name.Name) > 3 && strings.HasPrefix(d.Name.Name, "Get") && unicode.IsUpper(rune(d.Name.Name[3])) {
				name := d.Name.Name
				if d.Recv != nil {
					name = recvName(d) + "." + name
				}
				c.add("NAME-GETTER", at(d), name, fmt.Sprintf("%s has a Get prefix", name),
					fmt.Sprintf("Rename it to %s, or use a verb that says the call can block or fail (Fetch, Load, Lookup).", strings.TrimPrefix(d.Name.Name, "Get")))
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if !s.Name.IsExported() {
						continue
					}
					if repeats(s.Name.Name) {
						c.add("NAME-REPEAT", at(s), s.Name.Name, fmt.Sprintf("%s.%s repeats the package name", pkgName, s.Name.Name),
							fmt.Sprintf("Rename it to %s; callers already write %s.", strings.TrimPrefix(s.Name.Name, title), pkgName))
					}
					if sel, ok := s.Type.(*ast.SelectorExpr); ok && s.Assign.IsValid() {
						if x, ok := sel.X.(*ast.Ident); ok {
							c.add("NAME-ALIAS", at(s), s.Name.Name,
								fmt.Sprintf("%s.%s is an alias of %s.%s; one type has two names", pkgName, s.Name.Name, x.Name, sel.Sel.Name),
								fmt.Sprintf("Delete the alias and use %s.%s directly.", x.Name, sel.Sel.Name))
						}
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.IsExported() && repeats(n.Name) {
							c.add("NAME-REPEAT", at(n), n.Name, fmt.Sprintf("%s.%s repeats the package name", pkgName, n.Name),
								fmt.Sprintf("Rename it to %s; callers already write %s.", strings.TrimPrefix(n.Name, title), pkgName))
						}
					}
				}
			}
		}
	}
}

func recvName(d *ast.FuncDecl) string {
	t := d.Recv.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
		case *ast.IndexExpr:
			t = x.X
		case *ast.IndexListExpr:
			t = x.X
		case *ast.Ident:
			return x.Name
		default:
			return "?"
		}
	}
}

// checkThirdParty enforces one provenance note per vendored project.
func (c *checker) checkThirdParty() {
	dirs := map[string]map[string]bool{}
	for _, f := range c.files {
		parts := strings.SplitN(f, "/", 3)
		if parts[0] != "third_party" || len(parts) < 3 {
			continue
		}
		if dirs[parts[1]] == nil {
			dirs[parts[1]] = map[string]bool{}
		}
		dirs[parts[1]][parts[2]] = true
	}
	names := make([]string, 0, len(dirs))
	for d := range dirs {
		names = append(names, d)
	}
	sort.Strings(names)
	for _, d := range names {
		files := dirs[d]
		dir := "third_party/" + d
		if !files["CRUDE.md"] {
			c.add("THIRD-PARTY", dir, "-", "this folder has no CRUDE.md",
				"Add CRUDE.md: the upstream URL, the base version, every local change and how to update.")
		}
		license := false
		for f := range files {
			if !strings.Contains(f, "/") && strings.Contains(strings.ToUpper(f), "LICENSE") {
				license = true
			}
			if f == "CRUDE_PATCH.md" || f == "CRUDE_FORK.md" {
				c.add("THIRD-PARTY", dir+"/"+f, "-", "provenance notes are named CRUDE.md", "Rename it to CRUDE.md.")
			}
		}
		if !license {
			c.add("THIRD-PARTY", dir, "-", "this folder has no upstream license file", "Copy the upstream LICENSE file.")
		}
	}
}
