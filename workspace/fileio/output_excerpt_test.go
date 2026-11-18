package fileio

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf8"

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
		if utf8.ValidString(raw) && result.OutputEncoding != "" {
			t.Fatal("readable UTF-8 was encoded to satisfy an unrelated storage budget")
		}
	}
}

func TestCommandOutputPayloadOnlyEncodesNonUTF8Bytes(t *testing.T) {
	for _, raw := range []string{"", strings.Repeat("\x1b[m", 300) + "reason=quota\r\n", strings.Repeat("\a", DefaultOutputChunkBytes), "literal {\"output\":42}\n"} {
		output, encoding := commandOutputPayload(raw)
		if output != raw || encoding != "" {
			t.Fatal("command text was changed or unnecessarily encoded")
		}
	}
	raw := "A\xff\x00B"
	output, encoding := commandOutputPayload(raw)
	decoded, err := base64.StdEncoding.DecodeString(output)
	if encoding != "base64" || err != nil || string(decoded) != raw {
		t.Fatal("non-UTF-8 evidence was not preserved losslessly")
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
