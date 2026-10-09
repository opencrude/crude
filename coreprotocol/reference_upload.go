package coreprotocol

const (
	ReferenceUploadBegin   = "reference.upload-begin"
	ReferenceUploadChunk   = "reference.upload-chunk"
	ReferenceUploadCommit  = "reference.upload-commit"
	ReferenceUploadAbort   = "reference.upload-abort"
	ReferenceUploadCreate  = "create"
	ReferenceUploadReplace = "replace"
)

// Upload handles are scoped to the exact client epoch, workspace and session.
// Replacement preimages are server-read digests, never oversized original bytes.
type ReferenceUploadCommand struct {
	SessionID      string                  `json:"session_id"`
	UploadID       string                  `json:"upload_id,omitempty"`
	Purpose        string                  `json:"purpose,omitempty"`
	Create         *ReferenceCreateRequest `json:"create,omitempty"`
	ID             string                  `json:"id,omitempty"`
	Revision       string                  `json:"revision,omitempty"`
	OriginalDigest string                  `json:"original_digest,omitempty"`
	Size           int64                   `json:"size,omitempty"`
	Digest         string                  `json:"digest,omitempty"`
	Offset         int64                   `json:"offset,omitempty"`
	Data           []byte                  `json:"data,omitempty"`
}

type ReferenceUploadResult struct {
	UploadID string `json:"upload_id"`
	Next     int64  `json:"next"`
}
