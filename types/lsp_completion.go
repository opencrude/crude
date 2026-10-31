package types

// CompletionSnippet describes numeric placeholders relative to the expanded
// primary insertion text. It contains no editor state or protocol coordinates.
// Occurrences are in source preorder; Parent is -1 for a root occurrence.
// Stop 0 is the final landing, visited after positive stops in numeric order.
// If absent in the source, a root zero-width Stop 0 is synthesized at text end.
type CompletionSnippet struct {
	// TextSHA256 binds this initial plan to the one expanded primary insertion,
	// never raw snippet syntax or an edited session value. It is not authority.
	TextSHA256  string
	Occurrences []CompletionOccurrence
}

type CompletionOccurrence struct {
	Stop               int
	StartByte, EndByte int
	Parent             int
	Choices            []string
}
