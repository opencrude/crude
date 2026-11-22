package coreprotocol

// Bulk streams carry method-specific metadata once, then ordered byte chunks.
// Carrier credit, not an application round trip per chunk, bounds the producer.
const BulkChunkBytes = 256 << 10
const BulkMessageBytes = BulkChunkBytes + 64<<10

type BulkStart struct {
	Size     int64  `json:"size"`
	Digest   string `json:"digest"`
	Metadata []byte `json:"metadata"`
}

// BulkHeader precedes raw bytes in a message. DecodePrefix returns a zero-copy
// chunk valid until the next Reader.Next. Final messages contain no raw bytes;
// their digest authenticates the complete transfer, not merely the last chunk.
type BulkHeader struct {
	Seq    uint64 `json:"seq"`
	Offset int64  `json:"offset"`
	Final  bool   `json:"final"`
	Digest string `json:"digest"`
	Fault  *Fault `json:"fault,omitempty"`
}

// BulkReady describes the immutable transfer, with metadata encoded using the
// method's normal result type (FileChunk, ReferenceContentResult, or upload ID).
type BulkReady struct {
	Size     int64  `json:"size"`
	Metadata []byte `json:"metadata"`
	Fault    *Fault `json:"fault,omitempty"`
}
