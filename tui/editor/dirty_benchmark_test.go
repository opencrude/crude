package editor

import (
	"strings"
	"testing"
)

var dirtyBenchmarkSink bool

func BenchmarkBufferDirtyCheckLargeFile(b *testing.B) {
	pt := NewPieceTable()
	pt.ReplaceAll(strings.Repeat("0123456789abcdef\n", 100_000))
	buffer := newBuffer(pt, "large.txt", true)
	buffer.NoteEdit()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		dirtyBenchmarkSink = buffer.IsDirty()
	}
}
