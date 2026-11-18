package lspclient

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

const serverEvidenceBytes = 8 << 10
const serverEvidenceLines = 20
const serverEvidenceLineBytes = 1024

// serverEvidence belongs to one exact launch, including initialization failure.
// No log consumer or server callback can retain unbounded text or block pipe
// draining on I/O. Partial stderr lines are bounded too and remain readable.
type serverEvidence struct {
	mu           sync.Mutex
	lines        []string
	bytes        int
	pending      string
	truncated    bool
	errorMessage string
}

func evidenceText(text string) string {
	text = strings.TrimSpace(strings.ToValidUTF8(text, "�"))
	if len(text) <= serverEvidenceLineBytes {
		return text
	}
	end := serverEvidenceLineBytes - len(" … [truncated]")
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + " … [truncated]"
}

func (e *serverEvidence) appendLocked(line string) {
	if line == "" {
		return
	}
	line = evidenceText(line)
	e.lines = append(e.lines, line)
	e.bytes += len(line)
	for len(e.lines) > serverEvidenceLines || e.bytes > serverEvidenceBytes-serverEvidenceLineBytes {
		e.bytes -= len(e.lines[0])
		e.lines = slices.Delete(e.lines, 0, 1)
	}
}

func (e *serverEvidence) stderr(data []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for len(data) > 0 {
		part, rest, newline := bytes.Cut(data, []byte{'\n'})
		if !e.truncated {
			room := serverEvidenceLineBytes - len(e.pending)
			if len(part) > room {
				e.truncated = true
				part = part[:room]
			}
			e.pending += string(part)
		}
		if newline {
			line := e.pending
			if e.truncated {
				line += " … [truncated]"
			}
			if strings.TrimSpace(line) != "" {
				e.appendLocked("stderr: " + line)
			}
			e.pending, e.truncated = "", false
		}
		data = rest
	}
}

func (e *serverEvidence) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := slices.Clone(e.lines)
	if e.pending != "" {
		line := e.pending
		if e.truncated {
			line += " … [truncated]"
		}
		out = append(out, evidenceText("stderr: "+line))
	}
	if len(out) > serverEvidenceLines {
		out = out[len(out)-serverEvidenceLines:]
	}
	return out
}

// Evidence returns attributed server data, never instructions or proof of health.
// Stop preserves the tail; a new Start replaces it with a fresh launch's ring.
func (l *LSPIntegration) Evidence() []string {
	l.mu.Lock()
	evidence := l.evidence
	l.mu.Unlock()
	if evidence == nil {
		return nil
	}
	return evidence.snapshot()
}

// ErrorMessage is the latest unsolicited showMessage Error from the live launch.
// Command messages keep their existing command-capture behavior instead.
func (l *LSPIntegration) ErrorMessage() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.client == nil || l.launch == nil || l.stop != nil || l.launch.evidence == nil {
		return ""
	}
	l.launch.evidence.mu.Lock()
	defer l.launch.evidence.mu.Unlock()
	return l.launch.evidence.errorMessage
}

func (l *LSPIntegration) captureServerMessage(launch *lspLaunch, method string, params json.RawMessage) {
	if launch.evidence == nil || len(params) > 64<<10 {
		return
	}
	var message struct {
		Type    int    `json:"type"`
		Message string `json:"message"`
	}
	if json.Unmarshal(params, &message) != nil || (message.Type != 1 && message.Type != 2) {
		return
	}
	text := evidenceText(message.Message)
	if text == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.launch != launch {
		return
	}
	e := launch.evidence
	e.mu.Lock()
	defer e.mu.Unlock()
	level := "warning"
	if message.Type == 1 {
		level = "error"
	}
	e.appendLocked(method + " " + level + ": " + text)
	if method == "window/showMessage" && message.Type == 1 && l.command == nil {
		e.errorMessage = text
	}
}
