package viewmodel

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"crude/core/shellport"
	"crude/text/textencoding"
	"crude/tui/screens/findinfiles"
	"crude/workspace/fileio"
)

// fileSearchPreview owns one selected-row read, never a document or editor tab.
type fileSearchPreview struct {
	screen       *findinfiles.Screen
	generation   int
	path         string
	line, column int
	cancel       context.CancelFunc
}
type fileSearchPreviewResult struct {
	owner        *fileSearchPreview
	first        int
	text, notice string
}

func (m *App) retireFileSearchPreview() {
	if p := m.filePreview; p != nil {
		p.cancel()
		m.filePreview = nil
	}
}

func (m *App) syncFileSearchPreview() tea.Cmd {
	s := m.findInFiles
	if s == nil || s.Mode() != findinfiles.ModeFiles {
		m.retireFileSearchPreview()
		return nil
	}
	path := s.SelectedFilePath()
	if path == "" {
		m.retireFileSearchPreview()
		return nil
	}
	line, column := max(1, m.goToFileLanding.line), max(1, m.goToFileLanding.column)
	if p := m.filePreview; p != nil && p.screen == s && p.path == path && p.generation == s.Generation() && p.line == line && p.column == column {
		return nil
	}
	m.retireFileSearchPreview()
	ctx, cancel := context.WithCancel(context.Background())
	p := &fileSearchPreview{s, s.Generation(), path, line, column, cancel}
	m.filePreview = p
	full := m.chatFilePath(path)
	if m.BufferMgr != nil {
		buf := m.BufferMgr.Get(full)
		if buf == nil { // A Windows core publishes native document IDs even to a Unix shell.
			for ed := range m.liveEditors {
				if ed != nil && ed.FilePath() != "" && m.chatFilePath(ed.FilePath()) == full {
					buf = m.BufferMgr.Get(ed.FilePath())
					break
				}
			}
		}
		if buf != nil {
			result := p.excerpt(buf.LineCount(), buf.Line)
			m.applyFileSearchPreview(result)
			return nil
		}
	}
	files, remote := m.ws.(shellport.ClientFiles)
	external := m.goToFileLanding.external
	return m.clientIOCommand(ctx, func(ctx context.Context) tea.Msg {
		var text string
		var err error
		if remote {
			var raw, data []byte
			if limited, ok := files.(interface {
				ReadClientFileLimit(context.Context, string, bool, int) ([]byte, error)
			}); ok {
				raw, err = limited.ReadClientFileLimit(ctx, full, external, fileio.MaxProjectSearchFileSize)
			} else if external {
				raw, err = files.ReadExternalClientFile(ctx, full)
			} else {
				raw, err = files.ReadClientFile(ctx, full)
			}
			if err == nil {
				if fileio.IsBinary(raw) {
					return fileSearchPreviewResult{owner: p, notice: "This is a binary file."}
				}
				data, _, err = textencoding.DecodeText(raw)
				text = string(data)
			}
		} else {
			var preview *fileio.FilePreview
			preview, err = fileio.InspectFile(full, fileio.DefaultPreviewBytes, 0)
			if err == nil {
				if preview.Binary {
					return fileSearchPreviewResult{owner: p, notice: "This is a binary file."}
				}
				if preview.Truncated && strings.Count(preview.Text, "\n") < line {
					return fileSearchPreviewResult{owner: p, notice: "The cited line is beyond the preview limit."}
				}
				text = preview.Text
			}
		}
		if err != nil {
			return fileSearchPreviewResult{owner: p, notice: "Cannot read this file. Choose another file."}
		}
		if ctx.Err() != nil {
			return nil
		}
		lines := strings.Split(text, "\n")
		return p.excerpt(len(lines), func(i int) string { return strings.TrimSuffix(lines[i], "\r") })
	})
}

func (p *fileSearchPreview) excerpt(count int, at func(int) string) fileSearchPreviewResult {
	r := fileSearchPreviewResult{owner: p}
	if count == 0 || count == 1 && at(0) == "" {
		r.notice = "This file is empty."
		return r
	}
	if p.line > count {
		r.notice = fmt.Sprintf("The file ends at line %d.", count)
		return r
	}
	r.first = max(1, p.line-3)
	var lines []string
	for i := r.first - 1; i < min(count, p.line+3); i++ {
		s := at(i)
		if len(s) > 4096 {
			end := 4096
			for !utf8.RuneStart(s[end]) {
				end--
			}
			s = s[:end] + "…"
		}
		lines = append(lines, s)
	}
	r.text = strings.Join(lines, "\n")
	return r
}

func (m *App) applyFileSearchPreview(r fileSearchPreviewResult) {
	p := r.owner
	if p == nil || p != m.filePreview || p.screen != m.findInFiles || p.generation != p.screen.Generation() || p.path != p.screen.SelectedFilePath() {
		return
	}
	p.screen.SetFilePreview(r.text, r.first, p.line, p.column, r.notice)
	m.markProjectSearchDirty()
}
