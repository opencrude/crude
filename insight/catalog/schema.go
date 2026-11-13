package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// FormatVersion is the catalog document format. Packs must use the same value.
const FormatVersion = 1

// Document is the serialized catalog or pack. The embedded catalog and every
// pack use this one format. Entries name their sources by ID; Provenance
// resolves those IDs against Sources.
type Document struct {
	Format    int              `json:"format"`
	Sources   []SourceRecord   `json:"sources,omitempty"`
	Languages []LanguageRecord `json:"languages,omitempty"`
	Servers   []ServerRecord   `json:"servers,omitempty"`
	Packages  []PackageRecord  `json:"packages,omitempty"`
}

// SourceRecord identifies one upstream input and its exact revision.
type SourceRecord struct {
	ID       string `json:"id"`
	URL      string `json:"url,omitempty"`
	Revision string `json:"revision,omitempty"`
	Digest   string `json:"digest,omitempty"`
	License  string `json:"license,omitempty"`
}

type LanguageRecord struct {
	ID             string   `json:"id"`
	Name           string   `json:"name,omitempty"`
	Linguist       []string `json:"linguist,omitempty"`
	Extensions     []string `json:"extensions,omitempty"`
	Filenames      []string `json:"filenames,omitempty"`
	Interpreters   []string `json:"interpreters,omitempty"`
	LSPLanguageID  string   `json:"lsp_language_id,omitempty"`
	Roots          []string `json:"roots,omitempty"`
	WorkspaceRoots []string `json:"workspace_roots,omitempty"`
	Servers        []string `json:"servers,omitempty"`
	// Editor data. CommentTokens are line comment prefixes, the first is
	// preferred. Grammar names a syntax grammar; Crude uses it only when it
	// links a grammar of that name. Formatter reads the document on stdin
	// and writes the formatted text on stdout.
	CommentTokens []string         `json:"comment_tokens,omitempty"`
	BlockComment  *BlockComment    `json:"block_comment,omitempty"`
	Grammar       string           `json:"grammar,omitempty"`
	Formatter     *FormatterRecord `json:"formatter,omitempty"`
	// OffSide marks a language whose blocks end by dedent, not by a closing
	// token (VS Code language configuration folding.offSide). Indent guides
	// then give a blank line before a dedent the depth of the line below.
	OffSide bool `json:"off_side,omitempty"`
	// FoldingMarkers start and end a region that folds whatever its
	// indentation (VS Code language configuration folding.markers). Both are
	// RE2 patterns matched against one line without its terminator.
	FoldingMarkers *FoldingMarkers  `json:"folding_markers,omitempty"`
	Editing        *LanguageEditing `json:"editing,omitempty"`
	Sources        []string         `json:"sources,omitempty"`
}

// FoldingMarkers is a pair of region marker patterns.
type FoldingMarkers struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// BlockComment is a pair of block comment delimiters.
type BlockComment struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// FormatterRecord is a command that formats a document through standard
// input and output. The argument "{file}" is replaced by the document path,
// so the formatter can find its project configuration. It never receives
// write access to the file.
type FormatterRecord struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

type HintsRecord struct {
	Environment               map[string]string `json:"environment,omitempty"`
	Args                      []string          `json:"args,omitempty"`
	ArgPrefixes               []string          `json:"arg_prefixes,omitempty"`
	Settings                  map[string]any    `json:"settings,omitempty"`
	InitializationOptions     map[string]any    `json:"initialization_options,omitempty"`
	InitializationFromSection string            `json:"initialization_from_section,omitempty"`
}

type ServerRecord struct {
	ID                    string            `json:"id"`
	Command               string            `json:"command"`
	Args                  []string          `json:"args,omitempty"`
	Environment           map[string]string `json:"environment,omitempty"`
	Settings              map[string]any    `json:"settings,omitempty"`
	InitializationOptions map[string]any    `json:"initialization_options,omitempty"`
	Package               string            `json:"package,omitempty"`
	Hints                 *HintsRecord      `json:"hints,omitempty"`
	DiagnosticScope       string            `json:"diagnostic_scope,omitempty"`
	Note                  string            `json:"note,omitempty"`
	Sources               []string          `json:"sources,omitempty"`
}

type AssetRecord struct {
	Targets []string `json:"targets"`
	File    string   `json:"file"`
	Bin     string   `json:"bin,omitempty"`
}

type PackageSourceRecord struct {
	ID     string        `json:"id"`
	Assets []AssetRecord `json:"assets,omitempty"`
}

type PackageRecord struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Homepage    string              `json:"homepage,omitempty"`
	Licenses    []string            `json:"licenses,omitempty"`
	Languages   []string            `json:"languages,omitempty"`
	Source      PackageSourceRecord `json:"source"`
	Bin         map[string]string   `json:"bin,omitempty"`
	Sources     []string            `json:"sources,omitempty"`
}

// Diagnostic scopes a server can declare. They are declared data, not
// verified behavior.
const (
	ScopeProject   = "project"
	ScopeOpenFiles = "open-files"
)

// MaxDocumentBytes bounds one catalog or pack document.
const MaxDocumentBytes = 8 << 20

var (
	idPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9+#._-]{0,63}$`)
	envNamePattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	errInvalidInput = errors.New("catalog: invalid document")
)

// Decode parses one document strictly: unknown fields, trailing data and
// oversized input are errors.
func Decode(data []byte) (Document, error) {
	if len(data) > MaxDocumentBytes {
		return Document{}, fmt.Errorf("%w: document is larger than %d bytes", errInvalidInput, MaxDocumentBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var doc Document
	if err := decoder.Decode(&doc); err != nil {
		return Document{}, fmt.Errorf("%w: %v", errInvalidInput, err)
	}
	if decoder.More() {
		return Document{}, fmt.Errorf("%w: trailing data after document", errInvalidInput)
	}
	if doc.Format != FormatVersion {
		return Document{}, fmt.Errorf("%w: format %d is not supported; use %d", errInvalidInput, doc.Format, FormatVersion)
	}
	return doc, nil
}

// Encode writes a deterministic document: entries sorted by identity, maps
// sorted by encoding/json, two-space indentation and a final newline.
func Encode(doc Document) ([]byte, error) {
	doc.Format = FormatVersion
	sort.Slice(doc.Sources, func(i, j int) bool { return doc.Sources[i].ID < doc.Sources[j].ID })
	sort.Slice(doc.Languages, func(i, j int) bool { return doc.Languages[i].ID < doc.Languages[j].ID })
	sort.Slice(doc.Servers, func(i, j int) bool { return doc.Servers[i].ID < doc.Servers[j].ID })
	sort.Slice(doc.Packages, func(i, j int) bool { return doc.Packages[i].Name < doc.Packages[j].Name })
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func validateDocument(doc Document) error {
	sources := map[string]bool{}
	for _, source := range doc.Sources {
		if !idPattern.MatchString(source.ID) {
			return fmt.Errorf("%w: source id %q", errInvalidInput, source.ID)
		}
		if sources[source.ID] {
			return fmt.Errorf("%w: duplicate source %q", errInvalidInput, source.ID)
		}
		sources[source.ID] = true
	}
	seen := map[string]bool{}
	for _, language := range doc.Languages {
		if err := validateLanguage(language); err != nil {
			return err
		}
		if seen[language.ID] {
			return fmt.Errorf("%w: duplicate language %q", errInvalidInput, language.ID)
		}
		seen[language.ID] = true
	}
	seen = map[string]bool{}
	for _, server := range doc.Servers {
		if err := validateServer(server); err != nil {
			return err
		}
		if seen[server.ID] {
			return fmt.Errorf("%w: duplicate server %q", errInvalidInput, server.ID)
		}
		seen[server.ID] = true
	}
	seen = map[string]bool{}
	for _, pkg := range doc.Packages {
		if err := validatePackage(pkg); err != nil {
			return err
		}
		if seen[pkg.Name] {
			return fmt.Errorf("%w: duplicate package %q", errInvalidInput, pkg.Name)
		}
		seen[pkg.Name] = true
	}
	return nil
}

func validateLanguage(language LanguageRecord) error {
	if !idPattern.MatchString(language.ID) {
		return fmt.Errorf("%w: language id %q", errInvalidInput, language.ID)
	}
	if strings.TrimSpace(language.LSPLanguageID) == "" || len(language.LSPLanguageID) > 64 || strings.ContainsAny(language.LSPLanguageID, " \t\r\n\x00") {
		return fmt.Errorf("%w: language %s needs a valid lsp_language_id", errInvalidInput, language.ID)
	}
	if len(language.Extensions) == 0 && len(language.Filenames) == 0 && len(language.Interpreters) == 0 {
		return fmt.Errorf("%w: language %s needs extensions, filenames or interpreters", errInvalidInput, language.ID)
	}
	for _, token := range language.CommentTokens {
		if token == "" || len(token) > 16 || strings.ContainsAny(token, "\r\n\x00") {
			return fmt.Errorf("%w: language %s comment token %q", errInvalidInput, language.ID, token)
		}
	}
	if b := language.BlockComment; b != nil && (b.Start == "" || b.End == "" || len(b.Start) > 16 || len(b.End) > 16 ||
		strings.ContainsAny(b.Start+b.End, "\r\n\x00")) {
		return fmt.Errorf("%w: language %s block comment", errInvalidInput, language.ID)
	}
	if m := language.FoldingMarkers; m != nil {
		for _, pattern := range []string{m.Start, m.End} {
			if _, err := regexp.Compile(pattern); pattern == "" || len(pattern) > 256 || strings.ContainsAny(pattern, "\r\n\x00") || err != nil {
				return fmt.Errorf("%w: language %s folding marker %q", errInvalidInput, language.ID, pattern)
			}
		}
	}
	if err := validateEditing(language.Editing); err != nil {
		return fmt.Errorf("%w: language %s editing: %v", errInvalidInput, language.ID, err)
	}
	if language.Grammar != "" && !idPattern.MatchString(language.Grammar) {
		return fmt.Errorf("%w: language %s grammar %q", errInvalidInput, language.ID, language.Grammar)
	}
	if f := language.Formatter; f != nil {
		if strings.TrimSpace(f.Command) == "" || strings.ContainsAny(f.Command, "\x00\r\n") {
			return fmt.Errorf("%w: language %s formatter needs a command", errInvalidInput, language.ID)
		}
		for _, arg := range f.Args {
			if strings.ContainsRune(arg, 0) {
				return fmt.Errorf("%w: language %s formatter argument", errInvalidInput, language.ID)
			}
		}
	}
	for _, ext := range language.Extensions {
		if !validExtension(ext) {
			return fmt.Errorf("%w: language %s extension %q must be lowercase, start with a dot and contain no path separators", errInvalidInput, language.ID, ext)
		}
	}
	for _, name := range language.Filenames {
		if !validPattern(name) {
			return fmt.Errorf("%w: language %s filename pattern %q", errInvalidInput, language.ID, name)
		}
	}
	for _, list := range [][]string{language.Roots, language.WorkspaceRoots} {
		for _, marker := range list {
			if !validPattern(marker) {
				return fmt.Errorf("%w: language %s root marker %q must be a file name or file name pattern", errInvalidInput, language.ID, marker)
			}
		}
	}
	for _, name := range language.Interpreters {
		if name == "" || len(name) > 64 || strings.ContainsAny(name, "/\\ \t\r\n\x00") {
			return fmt.Errorf("%w: language %s interpreter %q", errInvalidInput, language.ID, name)
		}
	}
	for _, name := range language.Linguist {
		if strings.TrimSpace(name) == "" || len(name) > 64 {
			return fmt.Errorf("%w: language %s linguist name %q", errInvalidInput, language.ID, name)
		}
	}
	for _, server := range language.Servers {
		if !idPattern.MatchString(server) {
			return fmt.Errorf("%w: language %s server reference %q", errInvalidInput, language.ID, server)
		}
	}
	return nil
}

func validateServer(server ServerRecord) error {
	if !idPattern.MatchString(server.ID) {
		return fmt.Errorf("%w: server id %q", errInvalidInput, server.ID)
	}
	if strings.TrimSpace(server.Command) == "" || strings.ContainsRune(server.Command, 0) {
		return fmt.Errorf("%w: server %s needs a command", errInvalidInput, server.ID)
	}
	for _, arg := range server.Args {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("%w: server %s argument contains NUL", errInvalidInput, server.ID)
		}
	}
	if err := validateEnvironment(server.ID, server.Environment); err != nil {
		return err
	}
	switch server.DiagnosticScope {
	case "", ScopeProject, ScopeOpenFiles:
	default:
		return fmt.Errorf("%w: server %s diagnostic_scope %q must be project, open-files or empty", errInvalidInput, server.ID, server.DiagnosticScope)
	}
	if server.Package != "" && strings.TrimSpace(server.Package) == "" {
		return fmt.Errorf("%w: server %s package reference", errInvalidInput, server.ID)
	}
	if hints := server.Hints; hints != nil {
		if err := validateEnvironment(server.ID, hints.Environment); err != nil {
			return err
		}
		for _, arg := range append(append([]string(nil), hints.Args...), hints.ArgPrefixes...) {
			if arg == "" || strings.ContainsRune(arg, 0) {
				return fmt.Errorf("%w: server %s hint argument", errInvalidInput, server.ID)
			}
		}
		if section := hints.InitializationFromSection; section != "" {
			if _, ok := hints.Settings[section].(map[string]any); !ok {
				return fmt.Errorf("%w: server %s initialization_from_section %q must name an object in hint settings", errInvalidInput, server.ID, section)
			}
		}
	}
	return nil
}

func validateEnvironment(owner string, environment map[string]string) error {
	for name, value := range environment {
		if !envNamePattern.MatchString(name) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("%w: %s environment variable %q", errInvalidInput, owner, name)
		}
	}
	return nil
}

func validatePackage(pkg PackageRecord) error {
	if strings.TrimSpace(pkg.Name) == "" || len(pkg.Name) > 128 || strings.ContainsAny(pkg.Name, "/\\\x00") {
		return fmt.Errorf("%w: package name %q", errInvalidInput, pkg.Name)
	}
	if !strings.HasPrefix(pkg.Source.ID, "pkg:") || strings.ContainsAny(pkg.Source.ID, " \t\r\n\x00") {
		return fmt.Errorf("%w: package %s source must be a package URL", errInvalidInput, pkg.Name)
	}
	for _, asset := range pkg.Source.Assets {
		if len(asset.Targets) == 0 || asset.File == "" {
			return fmt.Errorf("%w: package %s asset needs targets and a file", errInvalidInput, pkg.Name)
		}
	}
	return nil
}

func validExtension(ext string) bool {
	return len(ext) > 1 && len(ext) <= 64 && ext[0] == '.' && strings.ToLower(ext) == ext &&
		!strings.ContainsAny(ext, "/\\*?[] \t\r\n\x00") && !strings.Contains(ext, "..")
}

func validPattern(pattern string) bool {
	if pattern == "" || len(pattern) > 128 || filepath.IsAbs(pattern) || strings.ContainsAny(pattern, "/\\\x00") || strings.Contains(pattern, "..") {
		return false
	}
	_, err := filepath.Match(pattern, "file")
	return err == nil
}
