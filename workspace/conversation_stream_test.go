package workspace

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"crude/types/reasoning"
)

func TestConversationStreamImmediateIndependentReaders(t *testing.T) {
	w := newIngressWorkspace(t)
	c := w.AgentMgr.Get("root").State.Messages
	readers := []*Conversation{NewConversation(), NewConversation(), NewConversation()}
	want := ""
	for i := 0; i < 600; i++ {
		text := []string{"hello ", "world\n", "界🌍", "some more text "}[i%4]
		want += text
		if !w.ApplyAgentEvent(ingressEnvelope("root", "session", "stream_token", uint64(i+1), text)).Accepted {
			t.Fatal("stream rejected")
		}
		if got := c.Messages(); len(got) != 1 || got[0].Content != want {
			t.Fatal("accepted text requires a tick or differs from source events")
		}
		for n, reader := range readers {
			if i%[]int{1, 17, 299}[n] != 0 && i != 599 {
				continue
			}
			d := c.ChangesSince(reader.Version())
			if reader.Version() != 0 && n != 2 && (len(d.Upserts) != 0 || len(d.Appends) != 1 || d.Order != nil) {
				t.Fatal("stream resent a whole message or transcript")
			}
			encoded, err := d.MarshalMsg(nil)
			if err != nil {
				t.Fatal(err)
			}
			var decoded ConversationDelta
			if rest, err := decoded.UnmarshalMsg(encoded); err != nil || len(rest) != 0 {
				t.Fatalf("wire decode: %v", err)
			}
			d = decoded
			if err := reader.InstallDelta(d); err != nil {
				t.Fatal(err)
			}
			if err := reader.InstallDelta(d); err != nil {
				t.Fatal("duplicate changed acceptance:", err)
			}
			if got := reader.Messages(); len(got) != 1 || got[0].Content != want {
				t.Fatalf("reader %d lost/duplicated text", n)
			}
		}
	}
}

func TestConversationStreamMalformedAtomic(t *testing.T) {
	for _, name := range []string{"gap", "overlap", "overflow", "missing", "duplicate", "conflict", "reset", "same-revision", "bad-utf8", "reasoning-index"} {
		t.Run(name, func(t *testing.T) {
			c := NewConversation()
			c.AppendMessage(Message{InternalID: "a", Role: MessageRoleAssistant, Content: "abc"})
			d := ConversationDelta{BaseRevision: 1, Revision: 2, Appends: []ConversationTextAppend{{InternalID: "a", Offset: 3, Text: "def"}}}
			switch name {
			case "gap":
				d.Appends[0].Offset = 4
			case "overlap":
				d.Appends[0].Offset = 2
			case "overflow":
				d.Appends[0].Offset = ^uint64(0)
			case "missing":
				d.Appends = append(d.Appends, ConversationTextAppend{InternalID: "absent", Text: "bad"})
			case "duplicate":
				d.Appends = append(d.Appends, d.Appends[0])
			case "conflict":
				d.Upserts = []Message{{InternalID: "a", Content: "replacement"}}
			case "reset":
				d.Reset, d.Order = true, []string{"a"}
			case "same-revision":
				d.Revision = 1
			case "bad-utf8":
				d.Appends[0].Text = "\xff"
			case "reasoning-index":
				d.Appends[0].ThinkingDisplay.Start = 100
			}
			before := c.Messages()
			if err := c.InstallDelta(d); err == nil {
				t.Fatal("malformed delta accepted")
			}
			if c.Version() != 1 || !reflect.DeepEqual(before, c.Messages()) {
				t.Fatal("malformed transaction partially applied")
			}
		})
	}
}

func TestConversationConflictingDuplicateAndStaleAppend(t *testing.T) {
	c := NewConversation()
	c.AppendMessage(Message{InternalID: "a", Role: MessageRoleAssistant, Content: "abc"})
	d := ConversationDelta{BaseRevision: 1, Revision: 2, Appends: []ConversationTextAppend{{InternalID: "a", Offset: 3, Text: "def"}}}
	if err := c.InstallDelta(d); err != nil {
		t.Fatal(err)
	}
	if err := c.InstallDelta(d); err != nil {
		t.Fatal(err)
	}
	d.Appends[0].Text = "BAD"
	if err := c.InstallDelta(d); err == nil {
		t.Fatal("conflicting duplicate accepted")
	}
	if c.Messages()[0].Content != "abcdef" {
		t.Fatal("duplicate changed canonical content")
	}
	if err := c.InstallDelta(ConversationDelta{BaseRevision: 2, Revision: 3, Appends: []ConversationTextAppend{{InternalID: "a", Offset: 6, Text: "ghi"}}}); err != nil {
		t.Fatal(err)
	}
	d.Appends[0].Text = "def"
	if err := c.InstallDelta(d); err == nil {
		t.Fatal("stale revision accepted")
	}
	if c.Messages()[0].Content != "abcdefghi" {
		t.Fatal("stale append lost newer text")
	}
}

func TestConversationReasoningAppendParity(t *testing.T) {
	c, replica := NewConversation(), NewConversation()
	var display reasoning.Display
	want := ""
	for _, delta := range []reasoning.Delta{
		{Text: "first", Kind: reasoning.Provisional, BlockID: "one"},
		{Text: " more", Kind: reasoning.Summary, BlockID: "one"},
		{Text: "next", Kind: reasoning.Summary, BlockID: "two"},
		{Text: " raw", Kind: reasoning.Raw},
	} {
		want = display.Append(want, delta)
		c.AppendReasoning(delta)
		d := c.ChangesSince(replica.Version())
		if err := replica.InstallDelta(d); err != nil {
			t.Fatal(err)
		}
		got := replica.Messages()[0]
		if got.Content != want || got.ThinkingDisplay != display {
			t.Fatalf("reasoning semantics differ: %+v, %q %+v", got, want, display)
		}
	}
	base := replica.Version()
	c.MarkAllThinkingInterrupted()
	if err := replica.InstallDelta(c.ChangesSince(base)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replica.Messages(), c.Messages()) {
		t.Fatal("settled reasoning differs")
	}
}

func FuzzConversationStreamReaders(f *testing.F) {
	f.Add([]byte{1, 4, 0, 7, 255, 13})
	f.Add([]byte(strings.Repeat("abcdef", 100)))
	f.Fuzz(func(t *testing.T, actions []byte) {
		if len(actions) > 2048 {
			actions = actions[:2048]
		}
		c := NewConversation()
		readers := []*Conversation{NewConversation(), NewConversation()}
		c.EnsureAssistant()
		want := ""
		for i, action := range actions {
			text := []string{"a", "界", "\n", "🌍"}[int(action)%4]
			want += text
			c.AppendToken(text)
			reader := readers[int(action)%2]
			if action%7 == 0 || i == len(actions)-1 {
				d := c.ChangesSince(reader.Version())
				if action%13 == 0 {
					d = c.ChangesSince(0)
				}
				if err := reader.InstallDelta(d); err != nil {
					t.Fatal(err)
				}
				if got := reader.Messages(); len(got) != 1 || got[0].Content != want {
					t.Fatal("replica differs from independently accumulated text")
				}
			}
		}
	})
}

func FuzzConversationMalformedTextOffset(f *testing.F) {
	f.Add(uint64(3), "next")
	f.Add(^uint64(0), "bad")
	f.Fuzz(func(t *testing.T, offset uint64, text string) {
		if len(text) > 4096 {
			text = text[:4096]
		}
		c := NewConversation()
		c.AppendMessage(Message{InternalID: "a", Role: MessageRoleAssistant, Content: "abc"})
		d := ConversationDelta{BaseRevision: 1, Revision: 2, Appends: []ConversationTextAppend{{InternalID: "a", Offset: offset, Text: text}}}
		before := c.Messages()
		err := c.InstallDelta(d)
		if err != nil {
			if c.Version() != 1 || !reflect.DeepEqual(c.Messages(), before) {
				t.Fatal("rejected append changed state")
			}
		} else if c.Version() != 2 || c.Messages()[0].Content != "abc"+text || offset != 3 || text == "" {
			t.Fatal("accepted append lost its fence or text")
		}
	})
}

func TestConversationAppendSteadyStateAllocations(t *testing.T) {
	c := NewConversation()
	c.EnsureAssistant()
	c.AppendToken("warm")
	c.textBuilder.Grow(2000)
	if allocs := testing.AllocsPerRun(1000, func() { c.AppendToken("x") }); allocs != 0 {
		t.Fatalf("steady-state append allocations = %v, want zero", allocs)
	}
}

func BenchmarkConversationPacedStreamWire(b *testing.B) {
	for _, flushBytes := range []int{256, 512, 1024} {
		b.Run(formatUint(uint64(flushBytes)), func(b *testing.B) {
			text := strings.Repeat("a", 20*1024)
			buffer := make([]byte, 0, 4096)
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				c := NewConversation()
				c.EnsureAssistant()
				initial := c.ChangesSince(0)
				buffer, _ = initial.MarshalMsg(buffer[:0])
				total := len(buffer)
				for start := 0; start < len(text); start += flushBytes {
					base := c.Version()
					end := min(start+flushBytes, len(text))
					// Tokens arrive immediately; only publication is paced.
					for at := start; at < end; at += 16 {
						c.AppendToken(text[at:min(at+16, end)])
					}
					d := c.ChangesSince(base)
					var err error
					buffer, err = d.MarshalMsg(buffer[:0])
					if err != nil {
						b.Fatal(err)
					}
					total += len(buffer)
				}
				if float64(total) >= 1.5*float64(len(text)) {
					b.Fatalf("wire bytes %d >= 1.5x %d", total, len(text))
				}
				b.ReportMetric(float64(total)/float64(len(text)), "wire/text")
			}
		})
	}
}

func TestConversationRandomStructuralStream(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	c, replica := NewConversation(), NewConversation()
	for i := 0; i < 1000; i++ {
		switch random.IntN(5) {
		case 0:
			c.AppendMessage(Message{Role: MessageRoleUser, Content: "next"})
		case 1:
			c.AppendThinking("thought")
		default:
			c.EnsureAssistant()
			c.AppendToken("token")
		}
		if random.IntN(17) == 0 {
			if err := replica.InstallDelta(c.ChangesSince(replica.Version())); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(replica.Messages(), c.Messages()) {
				t.Fatal("structural/text interleaving differs")
			}
		}
	}
}
