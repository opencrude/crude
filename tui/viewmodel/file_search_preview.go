package viewmodel

import (
	"context"
	"errors"
	"math"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/core/shellport"
	"crude/text/textencoding"
	"crude/tui/editor"
	"crude/tui/screens/findinfiles"
	"crude/workspace/fileio"
)

// A single selected file owns the read, independently of the match within it.
type fileSearchPreview struct {
	screen     *findinfiles.Screen
	generation int
	path       string
	buffer     *editor.Buffer
	revision   uint64
	cancel     context.CancelFunc
}
type fileSearchPreviewResult struct {
	owner  *fileSearchPreview
	file   *findinfiles.FilePreview
	notice string
}

func (m *App) retireFileSearchPreview() {
	if p := m.filePreview; p != nil {
		p.cancel()
		m.filePreview = nil
	}
}
func (m *App) syncFileSearchPreview() tea.Cmd {
	s := m.findInFiles
	if s == nil || s.SelectedFilePath() == "" {
		m.retireFileSearchPreview()
		return nil
	}
	path := s.SelectedFilePath()
	full := m.chatFilePath(path)
	var buf *editor.Buffer
	var revision uint64
	if m.BufferMgr != nil {
		buf = m.BufferMgr.Get(full)
		if buf == nil {
			buf = m.BufferMgr.Get(strings.ReplaceAll(full, "/", `\`))
		}
		if buf != nil {
			revision = buf.Revision()
		}
	}
	if p := m.filePreview; p != nil && p.screen == s && p.generation == s.Generation() && p.path == path && p.buffer == buf && p.revision == revision {
		return nil
	}
	m.retireFileSearchPreview()
	ctx, cancel := context.WithCancel(context.Background())
	p := &fileSearchPreview{s, s.Generation(), path, buf, revision, cancel}
	m.filePreview = p
	s.ClearFilePreview()
	files, remote := m.ws.(shellport.ClientFiles)
	external := m.goToFileLanding.external || s.LiteralFilePath() != ""
	var prepare func(context.Context) (*editor.PreviewDocument, error)
	if buf != nil {
		prepare = buf.CapturePreview(path)
	}
	results := s.Results()
	return m.clientIOCommand(ctx, func(ctx context.Context) tea.Msg {
		var document *editor.PreviewDocument
		out := fileSearchPreviewResult{owner: p}
		var err error
		if prepare != nil {
			document, err = prepare(ctx)
		} else {
			var raw []byte
			if remote {
				if external {
					raw, err = files.ReadExternalClientFile(ctx, full)
				} else {
					raw, err = files.ReadClientFile(ctx, full)
				}
			} else {
				raw, err = fileio.ReadRegularNoFollow(ctx, full, math.MaxInt64-1)
			}
			if err == nil {
				if fileio.IsBinary(raw) {
					out.notice = "This is a binary file."
					return out
				}
				var data []byte
				data, _, err = textencoding.DecodeText(raw)
				if err == nil {
					document, err = editor.PreparePreview(ctx, path, data, editor.DefaultTabWidth)
				}
			}
		}
		if err == nil {
			out.file, err = findinfiles.PrepareFilePreview(ctx, document, path, results)
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			out.notice = "Cannot read this file: " + err.Error()
		}
		return out
	})
}
func (m *App) applyFileSearchPreview(r fileSearchPreviewResult) {
	p := r.owner
	if p == nil || p != m.filePreview || p.screen != m.findInFiles || p.generation != p.screen.Generation() || p.path != p.screen.SelectedFilePath() || p.buffer != nil && p.buffer.Revision() != p.revision {
		return
	}
	p.screen.SetFilePreview(r.file, m.goToFileLanding.line, m.goToFileLanding.column, r.notice)
	m.markProjectSearchDirty()
}
