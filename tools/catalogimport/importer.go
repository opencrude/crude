package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"crude/insight/catalog"

	"github.com/BurntSushi/toml"
	"github.com/go-enry/go-enry/v2"
	enrydata "github.com/go-enry/go-enry/v2/data"
)

// Pins identifies the exact upstream inputs.
type Pins struct {
	Mason struct {
		Release, URL, SHA256, License string
	} `json:"mason"`
	Helix struct {
		Commit, URL, SHA256, License string
	} `json:"helix"`
}

// Report lists what the import skipped and why. Skips are expected: the
// catalog keeps only declarative fields that Crude can use.
type Report struct {
	Languages, Servers, Packages int
	Skipped                      []string
}

func (r *Report) skip(format string, args ...any) {
	r.Skipped = append(r.Skipped, fmt.Sprintf(format, args...))
}

const (
	sourceHelix    = "helix"
	sourceMason    = "mason-registry"
	sourceLinguist = "linguist"
	sourceAuthored = "crude-authored"
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+#._-]{0,63}$`)

type helixFile struct {
	LanguageServer map[string]helixServer `toml:"language-server"`
	Language       []helixLanguage        `toml:"language"`
}

type helixServer struct {
	Command     string            `toml:"command"`
	Args        []string          `toml:"args"`
	Environment map[string]string `toml:"environment"`
	Config      map[string]any    `toml:"config"`
}

type helixLanguage struct {
	Name            string   `toml:"name"`
	LanguageID      string   `toml:"language-id"`
	FileTypes       []any    `toml:"file-types"`
	Roots           []string `toml:"roots"`
	Shebangs        []string `toml:"shebangs"`
	LanguageServers []any    `toml:"language-servers"`
	CommentToken    any      `toml:"comment-token"`
	CommentTokens   any      `toml:"comment-tokens"`
	BlockComment    any      `toml:"block-comment-tokens"`
	Grammar         string   `toml:"grammar"`
	Formatter       *struct {
		Command string   `toml:"command"`
		Args    []string `toml:"args"`
	} `toml:"formatter"`
}

type masonPackage struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Homepage    string            `json:"homepage"`
	Licenses    []string          `json:"licenses"`
	Languages   []string          `json:"languages"`
	Categories  []string          `json:"categories"`
	Source      masonSource       `json:"source"`
	Bin         map[string]string `json:"bin"`
}

type masonSource struct {
	ID    string          `json:"id"`
	Asset json.RawMessage `json:"asset"`
}

// authoredFile is the hand-written overlay. Pointer fields distinguish an
// explicit empty list from an absent field.
type authoredFile struct {
	Note      string                      `json:"note"`
	Languages map[string]authoredLanguage `json:"languages"`
	Servers   map[string]authoredServer   `json:"servers"`
}

type authoredLanguage struct {
	Note             string    `json:"note"`
	Name             *string   `json:"name"`
	Linguist         *[]string `json:"linguist"`
	ExtensionsAdd    []string  `json:"extensions_add"`
	ExtensionsRemove []string  `json:"extensions_remove"`
	LSPLanguageID    *string   `json:"lsp_language_id"`
	Roots            *[]string `json:"roots"`
	WorkspaceRoots   *[]string `json:"workspace_roots"`
	Servers          *[]string `json:"servers"`
	ServersPrepend   []string  `json:"servers_prepend"`
	// Formatter replaces the upstream formatter; an empty command removes it.
	Formatter *catalog.FormatterRecord `json:"formatter"`
	OffSide   *bool                    `json:"off_side"`
	// FoldingMarkers replaces the region markers; it has no upstream source.
	FoldingMarkers *catalog.FoldingMarkers  `json:"folding_markers"`
	Editing        *catalog.LanguageEditing `json:"editing"`
}

type authoredLaunch struct {
	Command               string            `json:"command"`
	Args                  []string          `json:"args"`
	Environment           map[string]string `json:"environment"`
	Settings              map[string]any    `json:"settings"`
	InitializationOptions map[string]any    `json:"initialization_options"`
}

type authoredServer struct {
	Note            string               `json:"note"`
	Launch          *authoredLaunch      `json:"launch"`
	Package         *string              `json:"package"`
	DiagnosticScope string               `json:"diagnostic_scope"`
	Hints           *catalog.HintsRecord `json:"hints"`
}

// Import builds the catalog document from the pinned inputs. It is pure:
// the caller supplies verified bytes.
func Import(pins Pins, helixTOML, masonJSON, authoredJSON []byte) (catalog.Document, Report, error) {
	var report Report
	var helix helixFile
	if _, err := toml.NewDecoder(bytes.NewReader(helixTOML)).Decode(&helix); err != nil {
		return catalog.Document{}, report, fmt.Errorf("helix languages: %w", err)
	}
	var mason []masonPackage
	if err := json.Unmarshal(masonJSON, &mason); err != nil {
		return catalog.Document{}, report, fmt.Errorf("mason registry: %w", err)
	}
	var authored authoredFile
	decoder := json.NewDecoder(bytes.NewReader(authoredJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&authored); err != nil {
		return catalog.Document{}, report, fmt.Errorf("authored overlay: %w", err)
	}

	languages := map[string]*catalog.LanguageRecord{}
	helixByName := map[string]helixLanguage{}
	for _, hl := range helix.Language {
		helixByName[hl.Name] = hl
	}
	for _, hl := range helix.Language {
		// Languages without a language server stay: the editor still uses
		// their file types, comments, grammar and formatter.
		servers := helixServerNames(hl.LanguageServers)
		if !idPattern.MatchString(hl.Name) {
			report.skip("language %q: identifier is not valid", hl.Name)
			continue
		}
		record := &catalog.LanguageRecord{ID: hl.Name, LSPLanguageID: hl.Name, Servers: servers, Sources: []string{sourceHelix}}
		if hl.LanguageID != "" {
			record.LSPLanguageID = hl.LanguageID
		}
		for _, item := range hl.FileTypes {
			switch v := item.(type) {
			case string:
				ext := "." + v
				if ext != strings.ToLower(ext) {
					// An uppercase file type is a case-sensitive extension, such
					// as ".C" for C++. Keep it as a file name pattern. A whole
					// file name is kept only when Linguist knows it.
					if validPattern("*." + v) {
						record.Filenames = appendUnique(record.Filenames, "*."+v)
					}
					if len(enry.GetLanguagesByFilename(v, nil, nil)) > 0 && validPattern(v) {
						record.Filenames = appendUnique(record.Filenames, v)
					}
					continue
				}
				if !validExtension(ext) {
					report.skip("language %s: extension %q", hl.Name, ext)
					continue
				}
				record.Extensions = appendUnique(record.Extensions, ext)
				// A file type can also be a whole file name. Keep that meaning
				// only when Linguist knows such a file name.
				if len(enry.GetLanguagesByFilename(v, nil, nil)) > 0 && validPattern(v) {
					record.Filenames = appendUnique(record.Filenames, v)
				}
			case map[string]any:
				glob, _ := v["glob"].(string)
				if glob == "" || strings.ContainsAny(glob, "/\\") {
					report.skip("language %s: file pattern %v needs a path and is not supported", hl.Name, v)
					continue
				}
				if _, err := filepath.Match(glob, "file"); err != nil {
					report.skip("language %s: file pattern %q: %v", hl.Name, glob, err)
					continue
				}
				record.Filenames = appendUnique(record.Filenames, glob)
			}
		}
		record.CommentTokens = helixCommentTokens(hl)
		record.BlockComment = helixBlockComment(hl.BlockComment)
		record.Grammar = hl.Name
		if hl.Grammar != "" {
			record.Grammar = hl.Grammar
		}
		if !idPattern.MatchString(record.Grammar) {
			record.Grammar = ""
		}
		if hl.Formatter != nil && strings.TrimSpace(hl.Formatter.Command) != "" {
			record.Formatter = &catalog.FormatterRecord{Command: hl.Formatter.Command, Args: slices.Clone(hl.Formatter.Args)}
		}
		for _, interpreter := range hl.Shebangs {
			if validInterpreter(interpreter) {
				record.Interpreters = appendUnique(record.Interpreters, interpreter)
			}
		}
		for _, root := range hl.Roots {
			if validPattern(root) {
				record.Roots = append(record.Roots, root)
			} else {
				report.skip("language %s: root marker %q", hl.Name, root)
			}
		}
		languages[record.ID] = record
	}

	for id, patch := range authored.Languages {
		record := languages[id]
		if record == nil {
			return catalog.Document{}, report, fmt.Errorf("authored language %q is not in the upstream data", id)
		}
		if strings.TrimSpace(patch.Note) == "" {
			return catalog.Document{}, report, fmt.Errorf("authored language %q needs a note", id)
		}
		if patch.Name != nil {
			record.Name = *patch.Name
		}
		if patch.Linguist != nil {
			record.Linguist = slices.Clone(*patch.Linguist)
		}
		for _, ext := range patch.ExtensionsAdd {
			record.Extensions = appendUnique(record.Extensions, ext)
		}
		record.Extensions = slices.DeleteFunc(record.Extensions, func(ext string) bool { return slices.Contains(patch.ExtensionsRemove, ext) })
		if patch.LSPLanguageID != nil {
			record.LSPLanguageID = *patch.LSPLanguageID
		}
		if patch.Roots != nil {
			record.Roots = slices.Clone(*patch.Roots)
		}
		if patch.WorkspaceRoots != nil {
			record.WorkspaceRoots = slices.Clone(*patch.WorkspaceRoots)
		}
		if patch.Servers != nil {
			record.Servers = slices.Clone(*patch.Servers)
		}
		if patch.Formatter != nil {
			record.Formatter = nil
			if strings.TrimSpace(patch.Formatter.Command) != "" {
				record.Formatter = &catalog.FormatterRecord{Command: patch.Formatter.Command, Args: slices.Clone(patch.Formatter.Args)}
			}
		}
		if patch.OffSide != nil {
			record.OffSide = *patch.OffSide
		}
		if patch.FoldingMarkers != nil {
			markers := *patch.FoldingMarkers
			record.FoldingMarkers = &markers
		}
		if patch.Editing != nil {
			record.Editing = patch.Editing
		}
		if len(patch.ServersPrepend) > 0 {
			rest := slices.DeleteFunc(slices.Clone(record.Servers), func(s string) bool { return slices.Contains(patch.ServersPrepend, s) })
			record.Servers = append(slices.Clone(patch.ServersPrepend), rest...)
		}
		record.Sources = appendUnique(record.Sources, sourceAuthored)
	}

	// Linguist names. Each extension and exact file name votes for the
	// Linguist languages that claim it, split evenly when several do. A
	// Linguist language whose primary extension belongs to this entry gets a
	// strong extra vote. Only the top-voted names are kept, so an obscure
	// shared extension cannot attach an unrelated Linguist language.
	for _, record := range languages {
		if record.Linguist != nil {
			continue
		}
		votes := map[string]float64{}
		vote := func(names []string) {
			for _, name := range names {
				votes[name] += 1 / float64(len(names))
			}
		}
		for _, ext := range record.Extensions {
			vote(enry.GetLanguagesByExtension("file"+ext, nil, nil))
		}
		for _, pattern := range record.Filenames {
			if !strings.ContainsAny(pattern, "*?[") {
				vote(enry.GetLanguagesByFilename(pattern, nil, nil))
			}
		}
		best := 0.0
		for name := range votes {
			if primary := enry.GetLanguageExtensions(name); len(primary) > 0 && slices.Contains(record.Extensions, strings.ToLower(primary[0])) {
				votes[name] += 2
			}
			best = max(best, votes[name])
		}
		var names []string
		for name, score := range votes {
			// One whole vote is the least evidence: an unshared extension or
			// a primary extension. Split votes alone are too weak.
			if best >= 1 && score >= best-1e-9 {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		record.Linguist = names
		if len(names) > 0 {
			record.Sources = appendUnique(record.Sources, sourceLinguist)
		}
	}
	// Interpreters that Linguist assigns to exactly one Linguist language
	// identify scripts without an extension. They go only to the entry that
	// owns that language's primary extension, so variants that share the
	// Linguist name (for example a JSX identity) do not claim them.
	for interpreter, names := range enrydata.LanguagesByInterpreter {
		if len(names) != 1 || !validInterpreter(interpreter) {
			continue
		}
		primary := enry.GetLanguageExtensions(names[0])
		for _, record := range languages {
			if slices.Contains(record.Linguist, names[0]) && len(primary) > 0 && slices.Contains(record.Extensions, strings.ToLower(primary[0])) {
				record.Interpreters = appendUnique(record.Interpreters, interpreter)
			}
		}
	}
	// A shared extension stays with the one entry whose Linguist language
	// has it as the primary extension. Without exactly one such entry all
	// claimants keep it and detection picks the first by ID.
	claims := map[string][]*catalog.LanguageRecord{}
	for _, record := range languages {
		for _, ext := range record.Extensions {
			claims[ext] = append(claims[ext], record)
		}
	}
	for ext, owners := range claims {
		if len(owners) < 2 {
			continue
		}
		var primary []*catalog.LanguageRecord
		for _, record := range owners {
			for _, name := range record.Linguist {
				if exts := enry.GetLanguageExtensions(name); len(exts) > 0 && strings.ToLower(exts[0]) == ext {
					primary = append(primary, record)
					break
				}
			}
		}
		if len(primary) != 1 {
			continue
		}
		for _, record := range owners {
			if record != primary[0] {
				record.Extensions = slices.DeleteFunc(record.Extensions, func(e string) bool { return e == ext })
				report.skip("language %s: extension %s belongs to %s, whose Linguist language has it as primary", record.ID, ext, primary[0].ID)
			}
		}
	}
	for id, record := range languages {
		sort.Strings(record.Interpreters)
		if len(record.Extensions) == 0 && len(record.Filenames) == 0 && len(record.Interpreters) == 0 {
			report.skip("language %s: no file type that Crude can match", id)
			delete(languages, id)
			continue
		}
		if record.Name == "" {
			record.Name = record.ID
			if len(record.Linguist) == 1 {
				record.Name = record.Linguist[0]
			}
		}
		sort.Strings(record.Sources)
	}

	// Servers: every server that an included language names.
	byBin := map[string][]masonPackage{}
	masonByName := map[string]masonPackage{}
	for _, pkg := range mason {
		masonByName[pkg.Name] = pkg
		for bin := range pkg.Bin {
			byBin[bin] = append(byBin[bin], pkg)
		}
	}
	servers := map[string]*catalog.ServerRecord{}
	for _, record := range languages {
		kept := record.Servers[:0]
		for _, name := range record.Servers {
			_, upstream := helix.LanguageServer[name]
			_, authoredServer := authored.Servers[name]
			if !upstream && !authoredServer {
				report.skip("language %s: server %q has no launch definition upstream", record.ID, name)
				continue
			}
			kept = append(kept, name)
			if servers[name] != nil || !upstream {
				continue
			}
			hs := helix.LanguageServer[name]
			servers[name] = &catalog.ServerRecord{ID: name, Command: hs.Command, Args: hs.Args, Environment: hs.Environment, InitializationOptions: hs.Config, Sources: []string{sourceHelix}}
		}
		record.Servers = kept
		if len(kept) == 0 && len(helixServerNames(helixByName[record.ID].LanguageServers)) > 0 {
			report.skip("language %s: no server has a launch definition; the language stays for the editor", record.ID)
		}
	}
	for id, patch := range authored.Servers {
		server := servers[id]
		if server == nil {
			hs, ok := helix.LanguageServer[id]
			if !ok && patch.Launch == nil {
				return catalog.Document{}, report, fmt.Errorf("authored server %q is not in the upstream data and has no launch", id)
			}
			server = &catalog.ServerRecord{ID: id, Command: hs.Command, Args: hs.Args, Environment: hs.Environment, InitializationOptions: hs.Config}
			if ok {
				server.Sources = []string{sourceHelix}
			}
			servers[id] = server
		}
		if strings.TrimSpace(patch.Note) == "" {
			return catalog.Document{}, report, fmt.Errorf("authored server %q needs a note", id)
		}
		if l := patch.Launch; l != nil {
			server.Command, server.Args, server.Environment = l.Command, l.Args, l.Environment
			server.Settings, server.InitializationOptions = l.Settings, l.InitializationOptions
		}
		if patch.Package != nil {
			server.Package = *patch.Package
		}
		server.DiagnosticScope = patch.DiagnosticScope
		server.Hints = patch.Hints
		server.Note = patch.Note
		server.Sources = appendUnique(server.Sources, sourceAuthored)
	}

	packages := map[string]catalog.PackageRecord{}
	for _, server := range servers {
		if server.Package == "" {
			bin := filepath.Base(server.Command)
			candidates := byBin[bin]
			if len(candidates) > 1 {
				sort.Slice(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })
				chosen := -1
				for i, c := range candidates {
					if c.Name == server.ID {
						chosen = i
					}
				}
				if chosen < 0 {
					names := make([]string, 0, len(candidates))
					for _, c := range candidates {
						names = append(names, c.Name)
					}
					report.skip("server %s: several packages provide %q (%s); none chosen", server.ID, bin, strings.Join(names, ", "))
					continue
				}
				candidates = candidates[chosen : chosen+1]
			}
			if len(candidates) == 0 {
				continue
			}
			server.Package = candidates[0].Name
		}
		pkg, ok := masonByName[server.Package]
		if !ok {
			return catalog.Document{}, report, fmt.Errorf("server %s names package %q that the registry does not define", server.ID, server.Package)
		}
		if _, done := packages[pkg.Name]; done {
			continue
		}
		record := catalog.PackageRecord{
			Name: pkg.Name, Description: strings.TrimSpace(pkg.Description), Homepage: pkg.Homepage,
			Licenses: pkg.Licenses, Languages: pkg.Languages, Bin: pkg.Bin,
			Source: catalog.PackageSourceRecord{ID: pkg.Source.ID}, Sources: []string{sourceMason},
		}
		assets, unsupported := masonAssets(pkg.Source.Asset)
		if unsupported != "" {
			report.skip("package %s: %s", pkg.Name, unsupported)
		}
		record.Source.Assets = assets
		packages[pkg.Name] = record
	}

	doc := catalog.Document{Format: catalog.FormatVersion, Sources: []catalog.SourceRecord{
		{ID: sourceHelix, URL: pins.Helix.URL, Revision: pins.Helix.Commit, Digest: "sha256:" + pins.Helix.SHA256, License: pins.Helix.License},
		{ID: sourceMason, URL: pins.Mason.URL, Revision: pins.Mason.Release, Digest: "sha256:" + pins.Mason.SHA256, License: pins.Mason.License},
		{ID: sourceLinguist, URL: "https://github.com/go-enry/go-enry", Revision: "go-enry " + enrydata.LinguistCommit, License: "MIT (Linguist data), Apache-2.0 (go-enry)"},
		{ID: sourceAuthored, URL: "insight/catalog/authored.json", License: "MIT OR Apache-2.0"},
	}}
	for _, record := range languages {
		doc.Languages = append(doc.Languages, *record)
	}
	for _, server := range servers {
		sort.Strings(server.Sources)
		doc.Servers = append(doc.Servers, *server)
	}
	for _, record := range packages {
		doc.Packages = append(doc.Packages, record)
	}
	report.Languages, report.Servers, report.Packages = len(doc.Languages), len(doc.Servers), len(doc.Packages)
	sort.Strings(report.Skipped)
	// Validate through the runtime loader so the importer cannot produce a
	// document that the product rejects.
	encoded, err := catalog.Encode(doc)
	if err != nil {
		return catalog.Document{}, report, err
	}
	if _, err := catalog.Load(nil, encoded); err != nil {
		return catalog.Document{}, report, fmt.Errorf("generated catalog is invalid: %w", err)
	}
	return doc, report, nil
}

func helixServerNames(items []any) []string {
	var out []string
	for _, item := range items {
		switch v := item.(type) {
		case string:
			out = appendUnique(out, v)
		case map[string]any:
			if name, ok := v["name"].(string); ok {
				out = appendUnique(out, name)
			}
		}
	}
	return out
}

// masonAssets keeps only assets with one file and one string binary path:
// the forms that a direct download can install. Other forms are reported.
func masonAssets(raw json.RawMessage) ([]catalog.AssetRecord, string) {
	if len(raw) == 0 {
		return nil, ""
	}
	var items []map[string]any
	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, "asset list is malformed"
		}
	} else {
		var one map[string]any
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, "asset is malformed"
		}
		items = []map[string]any{one}
	}
	var out []catalog.AssetRecord
	var unsupported []string
	for _, item := range items {
		var targets []string
		switch t := item["target"].(type) {
		case string:
			targets = []string{t}
		case []any:
			for _, v := range t {
				if s, ok := v.(string); ok {
					targets = append(targets, s)
				}
			}
		}
		file, fileOK := item["file"].(string)
		bin, binOK := item["bin"].(string)
		if _, present := item["bin"]; !present {
			binOK = true
		}
		if len(targets) == 0 || !fileOK || !binOK {
			unsupported = appendUnique(unsupported, "an asset with several files, a binary map or no target")
			continue
		}
		out = append(out, catalog.AssetRecord{Targets: targets, File: file, Bin: bin})
	}
	return out, strings.Join(unsupported, "; ")
}

func appendUnique(list []string, value string) []string {
	if slices.Contains(list, value) {
		return list
	}
	return append(list, value)
}

func validExtension(ext string) bool {
	return len(ext) > 1 && len(ext) <= 64 && ext[0] == '.' && !strings.ContainsAny(ext, "/\\*?[] \t\r\n\x00") && !strings.Contains(ext, "..")
}

func helixCommentTokens(hl helixLanguage) []string {
	// Upstream writes either field as a string or a list of strings.
	var out []string
	for _, value := range []any{hl.CommentToken, hl.CommentTokens} {
		var tokens []any
		switch v := value.(type) {
		case string:
			tokens = []any{v}
		case []any:
			tokens = v
		}
		for _, item := range tokens {
			token, _ := item.(string)
			if token = strings.TrimSpace(token); token != "" && len(token) <= 16 {
				out = appendUnique(out, token)
			}
		}
	}
	return out
}

// helixBlockComment reads one table or the first of a list of tables.
func helixBlockComment(value any) *catalog.BlockComment {
	if list, ok := value.([]any); ok {
		if len(list) == 0 {
			return nil
		}
		value = list[0]
	}
	table, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	start, _ := table["start"].(string)
	end, _ := table["end"].(string)
	if start == "" || end == "" || len(start) > 16 || len(end) > 16 {
		return nil
	}
	return &catalog.BlockComment{Start: start, End: end}
}

func validInterpreter(name string) bool {
	return name != "" && len(name) <= 64 && !strings.ContainsAny(name, "/\\ \t\r\n\x00")
}

func validPattern(pattern string) bool {
	if pattern == "" || len(pattern) > 128 || filepath.IsAbs(pattern) || strings.ContainsAny(pattern, "/\\\x00") || strings.Contains(pattern, "..") {
		return false
	}
	_, err := filepath.Match(pattern, "file")
	return err == nil
}
