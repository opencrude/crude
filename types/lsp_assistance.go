package types

import "slices"

// LSPAssistanceCapabilities is the initialized server's static editing contract.
// The manager supplies Producer for the exact server attached to each open file.
// Reading this value never starts a server or sends a request.
type LSPAssistanceCapabilities struct {
	Producer            InsightProducer
	Completion          bool
	CompletionTriggers  []string
	SignatureHelp       bool
	SignatureTriggers   []string
	SignatureRetriggers []string
}

func CloneLSPAssistance(in LSPAssistanceCapabilities) LSPAssistanceCapabilities {
	in.CompletionTriggers = slices.Clone(in.CompletionTriggers)
	in.SignatureTriggers = slices.Clone(in.SignatureTriggers)
	in.SignatureRetriggers = slices.Clone(in.SignatureRetriggers)
	return in
}

const (
	LSPCompletionInvoked           = 1
	LSPCompletionTriggerCharacter  = 2
	LSPCompletionTriggerIncomplete = 3
	LSPSignatureInvoked            = 1
	LSPSignatureTriggerCharacter   = 2
	LSPSignatureContentChange      = 3
)

// Zero TriggerKind is a normal invocation. Producer, when supplied, fences an
// automatic input intent against replacement of the observed server.
type LSPCompletionContext struct {
	Producer         InsightProducer
	TriggerKind      int
	TriggerCharacter string
}

type LSPSignatureContext struct {
	Producer         InsightProducer
	TriggerKind      int
	TriggerCharacter string
	IsRetrigger      bool
	// Exact prior server result, not reconstructed display text. Only the
	// adapter encodes it as activeSignatureHelp after bounded validation.
	ActiveSignatureHelp []byte
}
