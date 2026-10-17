// Package gitdiff presents many Git comparisons as one continuous, hunk-focused
// document: every changed file stacked under a sticky header, unchanged runs
// folded into expandable gaps, unified or split. It owns no repository state;
// it paints immutable prepared files and emits typed file intents.
package gitdiff

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"crude/textdiff"
	"crude/tui/screens/diffview"
	"crude/workspace"
)

// ContextLines is how many unchanged lines surround each change.
const ContextLines = 3

// syntaxByteLimit skips lexing very large sources; they paint as plain text.
const syntaxByteLimit = 256 << 10

type lineKind uint8

const (
	lineContext lineKind = iota
	lineDeleted
	lineAdded
)

// line is one unified-presentation line; old/new are one-based, zero absent.
type line struct {
	kind     lineKind
	old, new int
}

// pair is one split-presentation row; either side may be absent.
type pair struct {
	old, new int
	changed  bool
}

// block is one display hunk: changes plus surrounding context.
type block struct {
	oldStart, newStart int // zero-based first line shown
	oldEnd, newEnd     int // zero-based exclusive end
	lines              []line
	pairs              []pair
}

// File is one prepared file section. Construct with Prepare*; it is immutable.
type File struct {
	Key        string
	Change     workspace.GitChange
	Comparison workspace.GitComparison
	Historical bool
	Note       string // replaces the body: binary, conflict, unreadable
	Added      int
	Removed    int

	before, after             []string
	beforeSyntax, afterSyntax [][]diffview.SyntaxSpan
	blocks                    []block
	width                     int // widest displayed source line
}

// Path is the repository-relative path shown for the file.
func (file *File) Path() string {
	if file.Comparison.Path != "" {
		return file.Comparison.Path
	}
	return file.Change.Path
}

// Code is the one-letter status shown beside the path.
func (file *File) Code() string {
	if file.Change.Code != "" {
		return file.Change.Code
	}
	switch {
	case file.Comparison.Deleted:
		return "D"
	case !file.Comparison.BeforePresent:
		return "A"
	case file.Comparison.OriginalPath != "":
		return "R"
	}
	return "M"
}

// FirstChangedLine is the one-based new-side line of the first change, for
// opening the file where the change is.
func (file *File) FirstChangedLine() int {
	for _, block := range file.blocks {
		for _, line := range block.lines {
			if line.kind != lineContext {
				return max(1, line.new, min(line.old, len(file.after)))
			}
		}
	}
	return 1
}

// ChangeKey is the identity of a working-tree change across snapshots.
func ChangeKey(change workspace.GitChange) string {
	return fmt.Sprintf("%d:%s", change.Area, change.Path)
}

// PrepareChanges derives presentation for a batched comparison. Call it off
// the UI goroutine: it diffs and lexes every file.
func PrepareChanges(items []workspace.GitChangeComparison) []File {
	files := make([]File, 0, len(items))
	for _, item := range items {
		file := File{Key: ChangeKey(item.Change), Change: item.Change}
		switch {
		case item.Change.Area == workspace.GitAreaConflict:
			file.Note = "Merge conflict · resolve the markers in the editor, then stage the file."
		case item.Err != nil:
			file.Note = "No comparison: " + item.Err.Error()
		default:
			file.Comparison = item.Comparison
			prepareBody(&file)
		}
		files = append(files, file)
	}
	// Display order matches the Git panel: conflicts, staged, then changes.
	sort.SliceStable(files, func(a, b int) bool { return areaRank(files[a].Change.Area) < areaRank(files[b].Change.Area) })
	return files
}

func areaRank(area workspace.GitChangeArea) int {
	switch area {
	case workspace.GitAreaConflict:
		return 0
	case workspace.GitAreaStaged:
		return 1
	}
	return 2
}

// PrepareComparisons derives presentation for historical comparisons (a
// commit's files). They are read-only: no stage, unstage or discard.
func PrepareComparisons(comparisons []workspace.GitComparison) []File {
	files := make([]File, 0, len(comparisons))
	for _, comparison := range comparisons {
		file := File{Key: "commit:" + comparison.Path, Comparison: comparison, Historical: true}
		file.Change = workspace.GitChange{Path: comparison.Path, AbsolutePath: comparison.AbsolutePath, Area: comparison.Area}
		prepareBody(&file)
		files = append(files, file)
	}
	return files
}

func prepareBody(file *File) {
	comparison := file.Comparison
	if comparison.Binary {
		file.Note = fmt.Sprintf("Binary file · %s → %s", byteSize(len(comparison.Before)), byteSize(len(comparison.After)))
		return
	}
	file.before, file.after = textdiff.Lines(comparison.Before), textdiff.Lines(comparison.After)
	if comparison.Before == comparison.After {
		switch {
		case comparison.BeforeMode != comparison.AfterMode:
			file.Note = fmt.Sprintf("File mode changed %06o → %06o · contents identical", comparison.BeforeMode, comparison.AfterMode)
		case len(file.after) == 0 && comparison.Untracked:
			file.Note = "New empty file"
		case len(file.after) == 0:
			file.Note = "Empty file"
		default:
			file.Note = "No textual changes"
		}
		return
	}
	if len(comparison.Before)+len(comparison.After) <= syntaxByteLimit {
		name := comparison.Path
		file.beforeSyntax = diffview.PrepareSyntax(name, comparison.Before)
		file.afterSyntax = diffview.PrepareSyntax(name, comparison.After)
	}
	file.blocks = buildBlocks(textdiff.ReviewHunks(comparison.Before, comparison.After), len(file.before), len(file.after))
	for _, block := range file.blocks {
		for _, line := range block.lines {
			text := ""
			switch line.kind {
			case lineAdded:
				file.Added++
				text = file.after[line.new-1]
			case lineDeleted:
				file.Removed++
				text = file.before[line.old-1]
			default:
				text = file.after[line.new-1]
			}
			file.width = max(file.width, diffview.SourceWidth(text))
		}
	}
}

// buildBlocks groups raw change hunks whose context would touch into display
// hunks, mapping unchanged lines old→new by the running offset.
func buildBlocks(hunks []textdiff.Hunk, beforeN, afterN int) []block {
	var blocks []block
	for start := 0; start < len(hunks); {
		end := start + 1
		for end < len(hunks) {
			previous := hunks[end-1]
			if hunks[end].OldStart-(previous.OldStart+len(previous.OldLines)) > 2*ContextLines {
				break
			}
			end++
		}
		group := hunks[start:end]
		first, last := group[0], group[len(group)-1]
		b := block{oldStart: max(0, first.OldStart-ContextLines)}
		b.newStart = first.NewStart - (first.OldStart - b.oldStart)
		oldCursor, newCursor := b.oldStart, b.newStart
		context := func(until int) {
			for oldCursor < until && newCursor < afterN {
				b.lines = append(b.lines, line{kind: lineContext, old: oldCursor + 1, new: newCursor + 1})
				b.pairs = append(b.pairs, pair{old: oldCursor + 1, new: newCursor + 1})
				oldCursor++
				newCursor++
			}
		}
		for _, hunk := range group {
			context(hunk.OldStart)
			oldCursor, newCursor = hunk.OldStart, hunk.NewStart
			for index := range hunk.OldLines {
				b.lines = append(b.lines, line{kind: lineDeleted, old: hunk.OldStart + index + 1})
			}
			for index := range hunk.NewLines {
				b.lines = append(b.lines, line{kind: lineAdded, new: hunk.NewStart + index + 1})
			}
			for index := 0; index < max(len(hunk.OldLines), len(hunk.NewLines)); index++ {
				p := pair{changed: true}
				if index < len(hunk.OldLines) {
					p.old = hunk.OldStart + index + 1
				}
				if index < len(hunk.NewLines) {
					p.new = hunk.NewStart + index + 1
				}
				b.pairs = append(b.pairs, p)
			}
			oldCursor += len(hunk.OldLines)
			newCursor += len(hunk.NewLines)
		}
		context(min(beforeN, last.OldStart+len(last.OldLines)+ContextLines))
		b.oldEnd, b.newEnd = oldCursor, newCursor
		blocks = append(blocks, b)
		start = end
	}
	return blocks
}

func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// BranchIcon is the branch glyph shared by the Git panel, review and header.
func BranchIcon(nerdFont bool) string {
	if nerdFont {
		return "\uf418" // nf-oct-git_branch
	}
	return "⑂"
}

// SplitPath separates a repository path into its name and parent directory.
func SplitPath(p string) (name, dir string) {
	p = strings.TrimSuffix(p, "/")
	dir, name = path.Split(p)
	return name, strings.TrimSuffix(dir, "/")
}
