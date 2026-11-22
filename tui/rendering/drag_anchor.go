package rendering

// CharacterDragAnchor selects the boundary of the source unit under the
// original press. A backward drag includes that unit. A forward drag starts
// at its leading edge. A caret, decoration or EOL supplies equal boundaries.
func CharacterDragAnchor[T any](leading, trailing T, backward bool) T {
	if backward {
		return trailing
	}
	return leading
}
