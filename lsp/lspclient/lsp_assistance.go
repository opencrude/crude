package lspclient

import (
	"crude/types/insight"
	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"maps"
	"slices"
	"unicode/utf8"
)

// Static initialize options are copied once into the manager's server lifetime.
// No transport object or mutable protocol slice crosses that boundary.
func (l *LSPIntegration) AssistanceCapabilities(path string) insight.LSPAssistanceCapabilities {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.client == nil {
		return insight.LSPAssistanceCapabilities{}
	}
	server := l.client.GetCapabilities()
	out := insight.LSPAssistanceCapabilities{}
	if c := l.completionOptionsLocked(path); c != nil {
		out.Completion = true
		out.CompletionTriggers = assistanceCharacters(c.TriggerCharacters)
	}
	if s := server.SignatureHelpProvider; s != nil {
		out.SignatureHelp = true
		out.SignatureTriggers = assistanceCharacters(s.TriggerCharacters)
		out.SignatureRetriggers = assistanceCharacters(s.RetriggerCharacters)
	}
	return out
}

func assistanceCharacters(in []string) []string {
	var out []string
	for _, value := range in {
		if utf8.ValidString(value) && utf8.RuneCountInString(value) == 1 && !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out
}

// OnAssistanceChanged notifies the owning producer after a registration change.
func (l *LSPIntegration) OnAssistanceChanged(f func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onAssistanceChanged = f
}

type registeredCompletion struct {
	selector []lsp.DiagnosticFilter
	options  protocol.CompletionOptions
}

func (l *LSPIntegration) completionOptionsLocked(path string) *protocol.CompletionOptions {
	p := l.client.GetCapabilities().CompletionProvider
	if l.launch == nil || len(l.launch.completions) == 0 {
		return p
	}
	doc := l.openFiles[path]
	if doc == nil {
		return p
	}
	var out protocol.CompletionOptions
	if p != nil {
		out = *p
		out.TriggerCharacters = slices.Clone(p.TriggerCharacters)
		out.AllCommitCharacters = slices.Clone(p.AllCommitCharacters)
	}
	found := p != nil
	for _, id := range slices.Sorted(maps.Keys(l.launch.completions)) {
		reg := l.launch.completions[id]
		if !selectorMatches(reg.selector, path, doc.languageID) {
			continue
		}
		found = true
		out.ResolveProvider = out.ResolveProvider || reg.options.ResolveProvider
		for _, v := range reg.options.TriggerCharacters {
			if !slices.Contains(out.TriggerCharacters, v) {
				out.TriggerCharacters = append(out.TriggerCharacters, v)
			}
		}
		for _, v := range reg.options.AllCommitCharacters {
			if !slices.Contains(out.AllCommitCharacters, v) {
				out.AllCommitCharacters = append(out.AllCommitCharacters, v)
			}
		}
	}
	if !found {
		return nil
	}
	return &out
}
