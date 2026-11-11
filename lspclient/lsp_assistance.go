package lspclient

import (
	"crude/types"
	"slices"
	"unicode/utf8"
)

// Static initialize options are copied once into the manager's server lifetime.
// No transport object or mutable protocol slice crosses that boundary.
func (l *LSPIntegration) AssistanceCapabilities() types.LSPAssistanceCapabilities {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.client == nil {
		return types.LSPAssistanceCapabilities{}
	}
	server := l.client.GetCapabilities()
	out := types.LSPAssistanceCapabilities{}
	if c := server.CompletionProvider; c != nil {
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
