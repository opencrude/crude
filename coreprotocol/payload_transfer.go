package coreprotocol

const (
	PayloadRead       = "payload.read"
	PayloadRelease    = "payload.release"
	MaxPayloadBytes   = 64 << 20
	PayloadChunkBytes = 256 << 10
)

// Payload access is ephemeral and private to one negotiated connection. It is
// intercepted by the transport and never admitted to the mutation journal.
type PayloadReadRequest struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
	Offset int64  `json:"offset"`
	Limit  int    `json:"limit"`
}

type PayloadReleaseRequest struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

type PayloadChunk struct {
	Data   []byte `json:"data"`
	Offset int64  `json:"offset"`
	Next   int64  `json:"next"`
	Total  int64  `json:"total"`
	Digest string `json:"digest"`
	EOF    bool   `json:"eof"`
}
