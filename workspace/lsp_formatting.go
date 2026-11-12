package workspace

import (
	"cmp"
	"context"
	"crude/document"
	"crude/lspedit"
	"crude/types"
	"errors"
	"path/filepath"
	"slices"
)

// Optional protocol support, not a new formatter/process owner.
type LSPRangeFormatter interface {
	RequestRangeFormatting(context.Context, string, types.LSPFormattingSpan, types.LSPFormattingOptions) (types.LSPFormattingResponse, error)
}

func formattingGate(s types.LSPSettings) bool { return s.Enabled }

func (w *Workspace) RequestSelectionFormatting(ctx context.Context, owner document.Owner, in types.LSPFormattingRequest) (types.LSPFormattingResult, error) {
	if w == nil || w.LiveDocuments == nil || w.LSPMgr == nil {
		return types.LSPFormattingResult{}, errors.New("selection formatting needs a language server")
	}
	in.Ranges = slices.Clone(in.Ranges)
	s := w.LiveDocuments
	s.mu.Lock()
	_, err := s.readOnlyLSPSourceLocked(owner, in.Source)
	s.mu.Unlock()
	if err != nil {
		return types.LSPFormattingResult{}, err
	}
	out, err := w.LSPMgr.selectionFormatting(ctx, in)
	if err != nil {
		return types.LSPFormattingResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err = s.readOnlyLSPSourceLocked(owner, in.Source); err != nil {
		return types.LSPFormattingResult{}, err
	}
	if _, err = w.LSPMgr.semanticFence(out.Boundary, out.WireVersion, formattingGate, "selection formatting"); err != nil {
		return types.LSPFormattingResult{}, err
	}
	return out, ctx.Err()
}
func (m *LSPManager) selectionFormatting(ctx context.Context, in types.LSPFormattingRequest) (types.LSPFormattingResult, error) {
	ctx, cancel := context.WithTimeout(ctx, ManualLSPRequestTimeout)
	defer cancel()
	var out types.LSPFormattingResult
	if !m.featureOn(formattingGate) {
		return out, errors.New("selection formatting needs enabled Code Intelligence")
	}
	err := m.trackInflight(func() error {
		if err := m.awaitSynchronized(ctx, in.Source.Path); err != nil {
			return err
		}
		client, request, valid, stop := m.requestClientWithin(ctx, in.Source.Path, ManualLSPRequestTimeout)
		defer stop()
		if client == nil {
			return errors.New("selection formatting needs a synchronized language server")
		}
		m.mu.Lock()
		doc := m.documents[filepath.Clean(in.Source.Path)]
		text := ""
		wire := 0
		var boundary types.InsightBoundary
		if doc != nil && doc.opened && !doc.syncPending {
			wire, text, boundary = doc.wireVersion, doc.text, doc.boundary
			boundary.Context.UnknownInputs = slices.Clone(boundary.Context.UnknownInputs)
		}
		m.mu.Unlock()
		if boundary.Source != in.Source {
			return document.ErrRevision
		}
		fence, err := m.semanticFence(boundary, wire, formattingGate, "selection formatting")
		if err != nil {
			return err
		}
		if err = validateFormattingRanges(text, in); err != nil {
			return err
		}
		out = types.LSPFormattingResult{Boundary: boundary, WireVersion: wire}
		formatter, supported := client.(LSPRangeFormatter)
		if !supported {
			out.Unsupported = true
			return fence()
		}
		var edits []document.Edit
		replacementBytes := 0
		for _, span := range in.Ranges {
			response, err := formatter.RequestRangeFormatting(request, in.Source.Path, span, in.Options)
			if request.Err() != nil {
				return request.Err()
			}
			if !valid() {
				return context.Canceled
			}
			if err != nil {
				return err
			}
			if err = fence(); err != nil {
				return err
			}
			if response.WireVersion != wire {
				return document.ErrRevision
			}
			if response.Unsupported {
				out.Unsupported = true
				return nil
			}
			wireEdits, err := lspedit.ParseTextEdits(response.JSON)
			if err != nil {
				return err
			}
			for _, edit := range wireEdits {
				a, err := lspedit.Offset(text, edit.Line, edit.Col)
				if err != nil {
					return err
				}
				b, err := lspedit.Offset(text, edit.EndLine, edit.EndCol)
				if err != nil {
					return err
				}
				if a < span.Start || b > span.End || a > b {
					return errors.New("the formatter changed text outside the selection; select a complete block and try again")
				}
				replacementBytes += len(edit.NewText)
				if replacementBytes > document.MaxBytes {
					return document.ErrLimit
				}
				edits = append(edits, document.Edit{Start: a, End: b, Text: edit.NewText})
				if len(edits) > document.MaxEdits {
					return document.ErrLimit
				}
			}
		}
		if len(edits) == 0 {
			return fence()
		}
		slices.SortFunc(edits, func(a, b document.Edit) int {
			if c := cmp.Compare(a.Start, b.Start); c != 0 {
				return c
			}
			return cmp.Compare(a.End, b.End)
		})
		if _, _, err := document.Apply([]byte(text), edits); err != nil {
			return err
		}
		out.Edits = edits
		return fence()
	})
	if err != nil {
		return types.LSPFormattingResult{}, err
	}
	return out, ctx.Err()
}
func validateFormattingRanges(text string, in types.LSPFormattingRequest) error {
	if len(in.Ranges) == 0 || len(in.Ranges) > 256 || in.Options.TabSize < 1 || in.Options.TabSize > 32 {
		return errors.New("select text and use a valid indentation width")
	}
	last := -1
	for _, span := range in.Ranges {
		if span.Start < last || span.End <= span.Start {
			return errors.New("formatting selections overlap or are empty")
		}
		if _, _, err := lspedit.Position(text, span.Start); err != nil {
			return err
		}
		if _, _, err := lspedit.Position(text, span.End); err != nil {
			return err
		}
		last = span.End
	}
	return nil
}
