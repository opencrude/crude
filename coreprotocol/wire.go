package coreprotocol

import (
	"encoding/json"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/tinylib/msgp/msgp"
)

// LSPEdit keeps the language server protocol's own JSON form on the wire;
// Crude has no mirror of the library's types.
type LSPEdit protocol.WorkspaceEdit

func (e *LSPEdit) MarshalMsg(b []byte) ([]byte, error) {
	text, err := json.Marshal((*protocol.WorkspaceEdit)(e))
	if err != nil {
		return b, err
	}
	return msgp.AppendBytes(b, text), nil
}

func (e *LSPEdit) UnmarshalMsg(b []byte) ([]byte, error) {
	text, rest, err := msgp.ReadBytesZC(b)
	if err != nil {
		return b, err
	}
	*e = LSPEdit{}
	return rest, json.Unmarshal(text, (*protocol.WorkspaceEdit)(e))
}

func (e *LSPEdit) Msgsize() int { return msgp.BytesPrefixSize + 256 }
