Here's the full, honest audit:



1\. MessageContent — Naive Value Type with Unexported Fields

Problem: MessageContent uses unexported fields (parts, text, isText) which means it can't be constructed with literal syntax from outside the package. This forced me to add TextContent() and PartsContent() constructors, and broke every single Content: "string" literal across the codebase (\~50+ call sites).



Better alternative: Use json.RawMessage or a custom Content field typed as any with a custom marshaler. This would allow Content: "string" to still compile while supporting Content: \[]ContentPart{...} at the API boundary only. The internal representation stays string; multi-part is assembled only when building the API request. This is what most OpenAI SDKs do — keep Content as string internally, and only construct the array-of-parts format during serialization.



Why I did it this way: I changed the type at the structural level to "make it impossible to accidentally send plain text when you meant multi-part." But this is over-engineering — the only place multi-part matters is the HTTP request body. The cost (50+ file changes, test breakage, every future ChatMessage construction requiring TextContent()) vastly exceeds the benefit.



2\. buildUserMessage — Loads ALL References from DB Every Turn

session/run.go:548:



refs, err := sessiondb.LoadPromptReferences(s.db)

Problem: On every user turn, buildUserMessage queries the database for all prompt references in the session, then builds a lookup map. For a session with hundreds of pastes and file attachments, this is O(N) DB query + O(N) map construction on every keystroke-submitted turn. Most references won't be referenced in the current message.



Better alternative: Pass the specific reference objects alongside the UserInput (not just IDs). The TUI already has the full reference objects in memory — it should send them through the channel, or at minimum query only the referenced IDs:



SELECT ... FROM prompt\_references WHERE id IN (?, ?, ?) AND deleted\_at IS NULL

3\. DB Content Column — Ambiguous JSON Detection via \[ Prefix

sessiondb/messages.go parseMessageContent:



if len(s) > 0 \&\& s\[0] == '\[' {

&#x20;   var parts \[]provider.ContentPart

&#x20;   if err := json.Unmarshal(\[]byte(s), \&parts); err == nil \&\& len(parts) > 0 {

Problem: Detecting multi-part content by checking if the DB text starts with \[ is fragile. A legitimate plain-text message that starts with \[ (e.g., \[SYSTEM] You are about to end...) could be misinterpreted as JSON and fail to parse, silently falling back to plain text. If it did happen to parse as valid JSON array (unlikely but not impossible), you'd get garbage content parts.



Better alternative: Add a content\_type column ("text" vs "multipart") to the messages table schema. Or prefix multi-part with a sentinel like \\x00MULTI:. Or use a separate content\_parts TEXT column that's NULL for plain text.



4\. Image Base64 Stored in Content Field — Memory/DB Bloat

promptref/promptref.go:24 — Content string holds base64 image data.



Problem: When a user pastes an image, the base64 data is stored in Reference.Content which gets written to the prompt\_references table's content TEXT column. A single 5MB image becomes \~6.7MB of base64 text stored in SQLite. For the KindPastedImage path in referenceStore.CreateReference, I do decode the base64 and write to a file on disk, then set Content = "". But:



If FilePath is already set (e.g., from the file picker selecting an image), the base64 content is never saved to file — it stays in Content and gets persisted to DB as a giant string

buildUserMessage reads ref.Content first, falling back to file reading — so for the image-via-file-picker path, it would try to use the empty Content and then read+re-encode the file every single turn

Better alternative: Always write image data to disk immediately in CreateReference, never store base64 in Content for images. The DB should only store the FilePath. This is what I did for KindPastedTextFile but incompletely for KindPastedImage.



5\. writeReferenceFile / writeReferenceBinary — DRY Violation

These two functions in references.go are 95% identical. The only difference is \[]byte(content) vs data. Classic code duplication.



6\. persistReference — Dead Code

references.go:196 — func (m \*App) persistReference(ref promptref.Reference) is never called. It was left over from an earlier design iteration. Should be deleted.



7\. FindRefRanges — O(N×M) Substring Search

tui/input/references.go:98-119:



for \_, ref := range refs {

&#x20;   label := ref.DisplayName()

&#x20;   for i := 0; i <= len(runes)-len(labelRunes); i++ {

&#x20;       if string(runes\[i:i+len(labelRunes)]) == label {

Problem: For each reference, it does a brute-force O(N) scan of the entire input text. With K references and text length N, this is O(N×K). Called on every keystroke via applyReferenceRanges(). For a large paste with many references, this could be slow.



Better alternative: Use a proper string search algorithm (Aho-Corasick for multi-pattern, or Boyer-Moore). Or since labels are user-visible bracketed text like \[Pasted Text1], just track positions when inserting rather than re-scanning.



Also: FindRefRanges doesn't handle overlapping matches or nested labels. If one reference's display name is a substring of another's, you could get overlapping ranges.



8\. PlaceholderFor / IsPlaceholderRune — Dead Code

tui/input/references.go:33-39: These functions are unused stubs from the earlier private-use-rune approach that was abandoned.



9\. Reference Label Collision

Problem: Labels like \[Pasted Text1] are generated with a per-kind counter that resets on every session resume (counter is in-memory only). After resume, the next paste creates \[Pasted Text1] again even though \[Pasted Text1] may already exist in the input buffer from the previous session. FindRefRanges would then find the wrong reference for one of them.



Better alternative: Include a nanosecond timestamp or UUID fragment in the label, or check for existing labels before assigning.



10\. AgentText() Still Expands to Text-Only

promptref/promptref.go:61-77: AgentText() for KindPastedImage returns:



Reference \[Pasted IMG1] (ref-123): pasted image saved at /path/to/file.png

This is the method used by ExpandReferences (the old text-only expansion path). It's now dead code for the submitInput path because buildUserMessage constructs ContentParts directly. But it's still called from ExpandedValue() which is used in the input submission. So the expanded text AND the multi-part message are both constructed — the text expansion is wasted work. The submitInput code sends the raw label text + reference IDs via UserInput, and buildUserMessage in session/run.go reconstructs the actual content. The ExpandedValue() call in update.go:60 is now redundant.



11\. No Provider Capability Check — All Providers Get Multi-Part

Problem: buildUserMessage constructs multi-part content regardless of whether the provider/model supports vision. Sending "content": \[{"type": "image\_url", ...}] to a text-only model (e.g., GPT-3.5, Claude Haiku without vision, or a local Llama model) will either error or silently drop the image.



Better alternative: Check provider.SupportsVision(model) or similar before constructing image content parts. Fall back to text-only path for non-vision models.



12\. UserInput Struct Lives in session Package — Cross-Dependency

session/input.go: The UserInput type is defined in the session package but used by tui/tui.go and tui/viewmodel. This creates a reverse dependency: the TUI now imports session. Previously, the interface EventInput was in session and implemented by TUI, which is a clean dependency inversion. But now tui/tui.go imports crude/session to use session.UserInput and session.InputWithRefs.



Better alternative: Define UserInput in a shared package (e.g., provider or a new session/input sub-package) that both session and tui can import without circular deps. Or keep it as an interface — EventInput could have a SendInput(UserInput) method instead of exposing the channel.



13\. Base64 Image Detection — Naive Heuristics

tui/input/update.go:16-17:



func isBase64Image(s string) bool {

&#x20;   return strings.HasPrefix(s, "data:image/") || (len(s) > 100 \&\& strings.HasPrefix(s, "/9j/"))

}

Problem:



/9j/ is the JPEG magic bytes in base64, but a regular text that happens to start with /9j/ would be misidentified (e.g., a file path like /9j/something)

The SanitizeForTerminal call runs before this check, which may strip or mangle base64 data

Terminals don't actually send base64 image data through tea.PasteMsg — they send text. Image pasting in terminals requires special protocol support (kitty image protocol, sixel, iTerm2 image protocol) which Bubble Tea v2 doesn't expose through PasteMsg

Reality: This code path will likely never fire. The only practical way to attach images is via the file picker (Ctrl+O). The base64 detection is speculative.



14\. No Content Size Limits on API Calls

Problem: A user pasting a 50MB image would cause buildUserMessage to read the entire file, base64-encode it, and embed it as a data URL in the API request. Most vision APIs have image size limits (OpenAI: 20MB per image, some much less). No validation or resizing happens.



15\. ContentString() Lossy for Multi-Part Messages

provider/types.go:37-51: MessageContent.String() (used by ContentString()) only extracts Type: "text" parts, discarding image parts. This means any code that reads ContentString() on a multi-part message gets an incomplete representation. While this is intentional for display purposes, it's a silent data loss that could surprise callers — particularly sanitizeMessages in session/messages.go which does ContentString() comparisons and string operations on what might be multi-part content.



16\. btwCh Still chan string

The btwCh channel still carries plain strings. If a user sends a "by the way" interruption while the agent is running, and the input had references, those references are lost — the BTW message goes as plain text without reference IDs. This is an inconsistency in the new UserInput pattern.



17\. Deleted References Are Soft-Deleted in Memory But Still Listed

referenceStore.ListReferences() returns all refs including soft-deleted ones (DeletedAt != 0). The ReferenceStore interface in tui/input/references.go doesn't filter by Active(). The FindRefRanges does check ref.Active(), but ListReferences callers may not. The in-memory refs map keeps deleted references forever for the session lifetime — a minor memory leak.



18\. imageMimeFromExt Duplicated

This function exists in session/run.go and a similar one (detectImageMime) in tui/input/update.go. The logic for mapping extensions to MIME types is scattered across three locations (these two + the viewmodel's ext-to-mime mapping in addFileReference).



19\. No Tests for Multi-Part Content

None of the new multi-modal paths have tests. The buildUserMessage function, MessageContent marshal/unmarshal, parseMessageContent DB round-trip, image file creation, and base64 encoding/decoding are all untested.



20\. referenceStore.AddExisting Doesn't Restore Counter

references.go:134-139: When resuming a session, AddExisting loads references from DB into the in-memory map, but doesn't update s.counter. So the next KindPastedText paste would create \[Pasted Text1] again, colliding with the resumed reference

