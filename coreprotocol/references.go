package coreprotocol

import "crude/sessiondb"

const (
	ReferenceCreate         = "reference.create"
	ReferenceImport         = "reference.import"
	ReferenceReadContent    = "reference.read-content"
	ReferenceSaveContent    = "reference.save-content"
	ReferenceReadMedia      = "reference.read-media"
	ReferenceRename         = "reference.rename"
	ReferenceDelete         = "reference.delete"
	ReferenceList           = "reference.list"
	HistoryList             = "history.list"
	HistoryRecord           = "history.record"
	HistoryRestore          = "history.restore"
	HistoryReadMedia        = "history.read-media"
	ReferenceMaxBytes       = 64 << 20
	ReferenceUploadMaxBytes = 4 << 20
	ReferenceChunkBytes     = 256 << 10
)

// Metadata paths identify server resources; clients must never open them locally.
// Revision fences the complete server-owned reference, not a projection cache.
type ReferenceView struct {
	Reference sessiondb.Reference `json:"reference"`
	Revision  string              `json:"revision"`
}
type ReferenceCreateRequest struct {
	Kind       sessiondb.Kind `json:"kind"`
	Label      string         `json:"label,omitempty"`
	Title      string         `json:"title,omitempty"`
	Mime       string         `json:"mime,omitempty"`
	Data       []byte         `json:"data,omitempty"`
	ServerPath string         `json:"server_path,omitempty"`
	Inline     bool           `json:"inline"`
}
type ReferenceCommand struct {
	SessionID string                  `json:"session_id"`
	ID        string                  `json:"id,omitempty"`
	Revision  string                  `json:"revision,omitempty"`
	Create    *ReferenceCreateRequest `json:"create,omitempty"`
	Original  string                  `json:"original,omitempty"`
	Content   string                  `json:"content,omitempty"`
	Label     string                  `json:"label,omitempty"`
	Offset    int64                   `json:"offset,omitempty"`
	Digest    string                  `json:"digest,omitempty"`
}
type ReferenceContentResult struct {
	View     ReferenceView `json:"view"`
	Data     []byte        `json:"data"`
	ReadOnly bool          `json:"read_only"`
	Digest   string        `json:"digest"`
	Total    int64         `json:"total"`
	Next     int64         `json:"next"`
}
type ReferenceListResult struct {
	References []ReferenceView `json:"references"`
}
type HistoryAttachment struct {
	Start       int           `json:"start"`
	End         int           `json:"end"`
	Reference   ReferenceView `json:"reference"`
	Unavailable bool          `json:"unavailable,omitempty"`
}
type HistoryEntry struct {
	ID          string              `json:"id"`
	Text        string              `json:"text"`
	Attachments []HistoryAttachment `json:"attachments,omitempty"`
	Count       int                 `json:"count"`
	FirstSeen   int64               `json:"first_seen"`
	LastSeen    int64               `json:"last_seen"`
}
type HistoryCommand struct {
	SessionID       string              `json:"session_id"`
	EntryID         string              `json:"entry_id,omitempty"`
	Text            string              `json:"text,omitempty"`
	Attachments     []HistoryAttachment `json:"attachments,omitempty"`
	AttachmentIndex int                 `json:"attachment_index,omitempty"`
	Offset          int64               `json:"offset,omitempty"`
	Digest          string              `json:"digest,omitempty"`
}
type HistoryListResult struct {
	Entries []HistoryEntry `json:"entries"`
}
type HistoryRestoreResult struct {
	Text         string          `json:"text"`
	ReferenceIDs []string        `json:"reference_ids"`
	References   []ReferenceView `json:"references"`
}
