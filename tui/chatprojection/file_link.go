package chatprojection

// ChatFileOpenActionID opens a source reference through the document owner.
const ChatFileOpenActionID = "chat.file-open"

// FileReference retains the producing turn’s managed checkout, not the currently selected checkout.
type FileReference struct{ Target, ExecutionID string }

// ChatWebOpenActionID asks before Crude opens a web link in the browser.
const ChatWebOpenActionID = "chat.web-open"
