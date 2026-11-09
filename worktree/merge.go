package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"crude/document"
	"crude/textdiff"
	"crude/textencoding"
)

func sourceDigest(p PlanDelta) string {
	if p.SourceDigest != "" {
		return p.SourceDigest
	}
	return p.Digest
}

// TextEdits returns exact line edits only after reconstructing the entire result.
// Diff failure is a conflict, never permission to omit a hunk.
func TextEdits(before, after []byte) ([]document.Edit, error) {
	if !utf8.Valid(before) || !utf8.Valid(after) || bytes.IndexByte(before, 0) >= 0 || bytes.IndexByte(after, 0) >= 0 {
		return nil, errors.New("non-text merge")
	}
	if bytes.Equal(before, after) {
		return nil, nil
	}
	lines := textdiff.Lines(string(before))
	edits := []document.Edit{}
	for _, h := range textdiff.Hunks(string(before), string(after)) {
		if h.OldStart > len(lines) || h.OldStart+len(h.OldLines) > len(lines) {
			return nil, errors.New("invalid diff")
		}
		start := len(strings.Join(lines[:h.OldStart], ""))
		end := start + len(strings.Join(h.OldLines, ""))
		if string(before[start:end]) != strings.Join(h.OldLines, "") {
			return nil, errors.New("inexact diff")
		}
		replacement := []byte(strings.Join(h.NewLines, ""))
		// Retain equal context (especially line terminators) outside the edit's
		// ownership span so adjacent line changes do not falsely overlap.
		old := before[start:end]
		prefix := 0
		for prefix < len(old) && prefix < len(replacement) && old[prefix] == replacement[prefix] {
			prefix++
		}
		for prefix > 0 && prefix < len(old) && !utf8.RuneStart(old[prefix]) {
			prefix--
		}
		suffix := 0
		for suffix < len(old)-prefix && suffix < len(replacement)-prefix && old[len(old)-suffix-1] == replacement[len(replacement)-suffix-1] {
			suffix++
		}
		for suffix > 0 && !utf8.RuneStart(old[len(old)-suffix]) {
			suffix--
		}
		edits = append(edits, document.Edit{Start: start + prefix, End: end - suffix, Text: string(replacement[prefix : len(replacement)-suffix])})
	}
	actual, _, err := document.Apply(before, edits)
	if err != nil || !bytes.Equal(actual, after) {
		return nil, errors.New("incomplete diff")
	}
	return edits, nil
}

// MergeTextBytes rebases text edits while retaining the independently selected
// disk encoding. Two conflicting encoding changes are a real merge conflict;
// a one-sided conversion does not prevent merging unrelated text edits.
func MergeTextBytes(base, proposed, current []byte) ([]byte, error) {
	b, be, err := textencoding.DecodeText(base)
	if err != nil {
		return nil, err
	}
	p, pe, err := textencoding.DecodeText(proposed)
	if err != nil {
		return nil, err
	}
	c, ce, err := textencoding.DecodeText(current)
	if err != nil {
		return nil, err
	}
	encoding := ce
	if ce == be {
		encoding = pe
	} else if pe != be && pe != ce {
		return nil, errors.New("both versions changed the file encoding")
	}
	wanted, err := TextEdits(b, p)
	if err != nil {
		return nil, err
	}
	newer, err := TextEdits(b, c)
	if err != nil {
		return nil, err
	}
	rebased, err := document.Rebase(wanted, newer)
	if err != nil {
		return nil, err
	}
	if len(rebased) != 0 {
		c, _, err = document.Apply(c, rebased)
		if err != nil {
			return nil, err
		}
	}
	return textencoding.EncodeText(c, encoding)
}

type Conflict struct {
	Path                    string
	Base, Current, Proposed *File
}

func (c *Conflict) Error() string { return "destination comparison required: " + c.Path }

// Resolve reviews the source and binds a merged plan to exact current destination
// bytes and revisions. Only strictly nonoverlapping exact text edits are merged.
func (s *Service) Resolve(ctx context.Context, id string, docs map[string]Document) (PlanDelta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolve(ctx, id, docs)
}
func (s *Service) resolve(ctx context.Context, id string, docs map[string]Document) (PlanDelta, error) {
	p, err := s.review(ctx, id)
	if err != nil {
		return p, err
	}
	p.SourceDigest = p.Digest
	c := s.contexts[id]
	for i, ch := range p.Changes {
		current, err := s.read(s.root, c.CommonGitDir, ch.Path)
		if err != nil {
			return PlanDelta{}, err
		}
		if d, ok := docs[ch.Path]; ok {
			if d.Revision == 0 {
				return PlanDelta{}, errors.New("document revision required")
			}
			current = nil
			if d.Exists {
				current = &File{Type: "file", Mode: d.Mode, Data: append([]byte(nil), d.Data...), Hash: hash(d.Data), DocumentRevision: d.Revision}
			}
		}
		if !equal(current, ch.Before) {
			if current == nil || ch.Before == nil || ch.After == nil || current.Mode != ch.Before.Mode || ch.After.Mode != ch.Before.Mode {
				return PlanDelta{}, &Conflict{ch.Path, ch.Before, current, ch.After}
			}
			result, e := MergeTextBytes(ch.Before.Data, ch.After.Data, current.Data)
			if e != nil {
				return PlanDelta{}, fmt.Errorf("%w: %v", &Conflict{ch.Path, ch.Before, current, ch.After}, e)
			}
			f := clone(*ch.After)
			f.Data = result
			f.Hash = hash(result)
			p.Changes[i].After = &f
		}
		p.Changes[i].Before = current
	}
	p.Digest = planDigest(p)
	return p, nil
}

// DestinationFile captures actual disk bytes (never accepted document text).
func (s *Service) DestinationFile(path string) (*File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(s.root, "", path)
}
