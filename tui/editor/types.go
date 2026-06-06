package editor

type Position struct {
	Line int
	Col  int
}

type EditInfo struct {
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
}
