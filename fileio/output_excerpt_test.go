package fileio

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"crude/types/command"
)

func TestOutputExcerptPreservesRawControlBytesAndDoesNotMoveCursors(t *testing.T) {
	for _, raw := range []string{"\x1b[31mred\x1b[0m\r\n\tvalue\rprogress\n", "A界B\xff\x00C", strings.Repeat("\x00", DefaultOutputChunkBytes), strings.Repeat("\a", DefaultOutputChunkBytes), strings.Repeat("界", 30000) + "TAIL"} {
		state := &asyncCmdState{output: *bytes.NewBufferString(raw), readCursor: 3, outputCursor: 2}
		result := state.outputExcerpt(AsyncCmdInfo{Handle: "job", Status: AsyncCompleted})
		out := result.Output
		if result.OutputEncoding == "base64" {
			decoded, err := base64.StdEncoding.DecodeString(out)
			if err != nil {
				t.Fatal(err)
			}
			out = string(decoded)
		}
		if len(raw) <= DefaultOutputChunkBytes && out != raw {
			t.Fatalf("raw evidence was rewritten: %q", out)
		}
		if len(raw) > DefaultOutputChunkBytes && (len(out) > DefaultOutputChunkBytes+80 || !strings.HasSuffix(out, "TAIL") || result.OutputDescriptor.OmittedBytes == 0) {
			t.Fatal("excerpt is unbounded or lost tail/omission evidence")
		}
		if state.readCursor != 3 || state.outputCursor != 2 || result.OutputDescriptor.TotalBytes != len(raw) {
			t.Fatal("human audit consumed the model's output cursor or lost raw byte count")
		}
		encoded, err := json.Marshal(result)
		if err != nil || len(encoded) > DefaultOutputChunkBytes*2+1024 {
			t.Fatalf("bounded output overflowed journal serialization: %d bytes, %v", len(encoded), err)
		}
	}
}

func BenchmarkCommandOutputExcerpt(b *testing.B) {
	state := &asyncCmdState{output: *bytes.NewBufferString(strings.Repeat("build output\n", 1<<18))}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := state.outputExcerpt(AsyncCmdInfo{Handle: "build", Status: command.Completed})
		if result.OutputDescriptor.TotalBytes == 0 {
			b.Fatal("empty capture")
		}
	}
}
