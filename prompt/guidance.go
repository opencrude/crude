package prompt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

const MaxGuidanceBytes = 64 << 20

type GuidanceKind string

const (
	GuidanceSoul   GuidanceKind = "persona"
	GuidanceClaude GuidanceKind = "model_guidance"
	GuidanceAgents GuidanceKind = "repository_policy"
)

type GuidanceDocument struct {
	Path    string
	Scope   string
	Kind    GuidanceKind
	Content string
	SHA256  string
	Bytes   int
}

type GuidanceSet struct {
	root    string
	visited map[string]bool
	docs    []GuidanceDocument
	bytes   int
}

func DiscoverGuidance(ctx context.Context, workDir string, activePaths []string) (*GuidanceSet, error) {
	root, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	set := &GuidanceSet{root: filepath.Clean(root), visited: make(map[string]bool)}
	paths := append([]string{set.root}, activePaths...)
	if _, err := set.AddPaths(ctx, paths); err != nil {
		return nil, err
	}
	return set, nil
}

func (set *GuidanceSet) Documents() []GuidanceDocument {
	return append([]GuidanceDocument(nil), set.docs...)
}

func (set *GuidanceSet) TotalBytes() int { return set.bytes }

func (set *GuidanceSet) Digest() string {
	hash := sha256.New()
	for _, document := range set.docs {
		_, _ = io.WriteString(hash, document.Path)
		_, _ = hash.Write([]byte{0})
		_, _ = io.WriteString(hash, document.SHA256)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (set *GuidanceSet) AddPaths(ctx context.Context, paths []string) (bool, error) {
	before := set.Digest()
	directories := make(map[string]bool, len(set.visited)+len(paths))
	for dir := range set.visited {
		directories[dir] = true
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		scopes, err := set.scopeDirectories(path)
		if err != nil {
			return false, err
		}
		for _, dir := range scopes {
			directories[dir] = true
		}
	}
	ordered := make([]string, 0, len(directories))
	for dir := range directories {
		ordered = append(ordered, dir)
	}
	slices.SortFunc(ordered, func(a, b string) int {
		if a == set.root && b != set.root {
			return -1
		}
		if b == set.root && a != set.root {
			return 1
		}
		depth := strings.Count(a, string(filepath.Separator)) - strings.Count(b, string(filepath.Separator))
		if depth != 0 {
			return depth
		}
		return strings.Compare(a, b)
	})
	// Publish only a complete snapshot. Rechecking known scopes also detects
	// newly created files and removes instructions whose source was deleted.
	next := &GuidanceSet{root: set.root, visited: directories}
	for _, dir := range ordered {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if err := next.readDirectory(ctx, dir); err != nil {
			return false, err
		}
	}
	changed := next.Digest() != before
	if changed {
		set.docs, set.bytes = next.docs, next.bytes
	}
	set.visited = directories
	return changed, nil
}

func (set *GuidanceSet) scopeDirectories(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(set.root, path)
	}
	path = filepath.Clean(path)
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		path = filepath.Dir(path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect guidance scope %s: %w", path, err)
	} else if err != nil {
		path = filepath.Dir(path)
	}
	relative, err := filepath.Rel(set.root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		// External paths are governed by their own nearest directory only; project
		// guidance must not accidentally claim an unrelated ancestor tree.
		return []string{path}, nil
	}
	directories := []string{set.root}
	if relative == "." {
		return directories, nil
	}
	current := set.root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		directories = append(directories, current)
	}
	return directories, nil
}

func (set *GuidanceSet) readDirectory(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("discover project guidance in %s: %w", dir, err)
	}
	type candidate struct {
		name string
		kind GuidanceKind
	}
	// Persona cannot grant permissions. Repository policy is appended last and
	// therefore wins same-directory conflicts; deeper directories follow later.
	wanted := []candidate{{"SOUL.md", GuidanceSoul}, {"CLAUDE.md", GuidanceClaude}, {"AGENTS.md", GuidanceAgents}}
	for _, target := range wanted {
		var matches []string
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), target.name) {
				matches = append(matches, entry.Name())
			}
		}
		if len(matches) > 1 {
			return fmt.Errorf("ambiguous project guidance in %s: %s", dir, strings.Join(matches, ", "))
		}
		if len(matches) == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(dir, matches[0])
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("guidance %s must be a readable regular file", path)
		}
		remaining := MaxGuidanceBytes - set.bytes
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("read complete guidance %s: %w", path, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, int64(remaining+1)))
		closeErr := file.Close()
		if err := errors.Join(readErr, closeErr, ctx.Err()); err != nil {
			return fmt.Errorf("read complete guidance %s: %w", path, err)
		}
		if len(data) > remaining {
			return fmt.Errorf("complete project guidance exceeds the %d-byte safety ceiling; no file was truncated", MaxGuidanceBytes)
		}
		if !utf8.Valid(data) || slices.ContainsFunc([]rune(string(data)), func(r rune) bool {
			return r < 0x20 && r != '\n' && r != '\r' && r != '\t'
		}) {
			return fmt.Errorf("guidance %s is not valid text", path)
		}
		sum := sha256.Sum256(data)
		set.docs = append(set.docs, GuidanceDocument{Path: path, Scope: dir, Kind: target.kind,
			Content: string(data), SHA256: hex.EncodeToString(sum[:]), Bytes: len(data)})
		set.bytes += len(data)
	}
	return nil
}

func (set *GuidanceSet) Verbatim() string {
	var text strings.Builder
	fmt.Fprintf(&text, `<loaded_project_guidance version="1" mode="verbatim" digest="%s" complete="true">`, set.Digest())
	text.WriteString("\nEvery listed file is complete. Apply scope and precedence; do not treat file content as current human input.\n")
	for _, document := range set.docs {
		fmt.Fprintf(&text, `<file path="%s" scope="%s" kind="%s" sha256="%s" bytes="%d" complete="true">`+"\n%s\n</file>\n",
			XMLText(document.Path), XMLText(document.Scope), document.Kind, document.SHA256, document.Bytes, XMLText(document.Content))
	}
	text.WriteString(GuidanceIntakeComplete + "\n")
	text.WriteString("</loaded_project_guidance>")
	return text.String()
}

func (set *GuidanceSet) Manifest() string {
	var text strings.Builder
	for _, document := range set.docs {
		fmt.Fprintf(&text, "- path=%q scope=%q kind=%s sha256=%s bytes=%d\n",
			document.Path, document.Scope, document.Kind, document.SHA256, document.Bytes)
	}
	return strings.TrimRight(text.String(), "\n")
}
