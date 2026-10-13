package chatprojection

import (
	"fmt"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

// styledSpanCount counts spans that carry real styling (explicit colors or
// emphasis attrs), i.e. what turns into colored/derived paint.
func styledSpanCount(rows []PaintLine) int {
	n := 0
	for _, row := range rows {
		for _, s := range row.Spans {
			if s.FGSet || s.BGSet || s.BgSet || s.Attr != 0 || s.Role != ui.ColorFg {
				n++
			}
		}
	}
	return n
}

var markdownBody = "Here is **bold** and *italic* and `inline code`.\n\n```go\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n```\n\nDone."

func dumpItemRows(t *testing.T, tag string, r *Renderer) {
	t.Helper()
	idx := -1
	for i, m := range r.sync.Messages {
		if m.InternalID == "a1" {
			idx = i
			break
		}
	}
	_ = r.Render(120)
	item := r.vl.Item(idx)
	if item == nil {
		return
	}
	for ri, row := range item.Native {
		var b string
		for _, sp := range row.Spans {
			marker := ""
			if sp.Role != ui.ColorFg {
				marker += "role=" + string(sp.Role) + " "
			}
			if sp.Attr != 0 {
				marker += fmt.Sprintf("attr=%d ", sp.Attr)
			}
			if sp.FGSet {
				marker += "fg "
			}
			if sp.BGSet || sp.BgSet {
				marker += "bg "
			}
			b += "[" + sp.Text + " " + marker + "] "
		}
		t.Logf("%s row%d: %s", tag, ri, b)
	}
}

// TestColorPersistenceAcrossTurns drives a realistic two-turn cycle and
// asserts the first turn's assistant rows keep their styling on every frame.
func TestColorPersistenceAcrossTurns(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(120, 40)
	r.SetChatAreaBounds(0, 30)

	count := func(tag string, id string) int {
		idx := -1
		for i, m := range r.sync.Messages {
			if m.InternalID == "a1" {
				idx = i
				break
			}
		}
		_ = r.Render(120)
		if idx < 0 {
			return -1
		}
		item := r.vl.Item(idx)
		if item == nil {
			return -1
		}
		n := 0
		for _, row := range item.Native {
			n += styledSpanCount([]PaintLine{row})
		}
		if n == 0 {
			t.Logf("%s: WARN no styled spans a1", tag)
		}
		return n
	}

	// ---- Turn 1 ----
	r.SyncMessages([]types.ChatMessage{
		{InternalID: "u1", Role: types.RoleUser, Content: "write me some go", Time: time.Unix(1, 0)},
	}, 1)
	r.SyncMessages([]types.ChatMessage{
		{InternalID: "u1", Role: types.RoleUser, Content: "write me some go", Time: time.Unix(1, 0)},
		{InternalID: "a1", Role: types.RoleAssistant, Content: markdownBody, Time: time.Unix(2, 0)},
	}, 2)
	base := count("turn1-seed", "a1")
	fmt.Printf("turn1 base styled spans: %d\n", base)
	dumpItemRows(t, "base", r)
	if base <= 0 {
		t.Fatalf("expected markdown styling, got %d (markdown lost at first render)", base)
	}

	// Rebuilding the virtual item list used to preserve CachedLines but discard
	// the parallel nativeCache rows while marking the item clean. That produced
	// a colorless frame after structural turn changes.
	r.RebuildItems()
	if got := count("rebuild-items", "a1"); got != base {
		t.Fatalf("item rebuild lost styling: got %d want %d", got, base)
	}

	// A stale/missing native half must invalidate and re-render, never rebuild
	// plain ColorFg rows from cached text.
	delete(r.nativeCache, "a1")
	r.RebuildItems()
	if got := count("missing-native-cache", "a1"); got != base {
		t.Fatalf("native cache repair lost styling: got %d want %d", got, base)
	}
	if cached := r.nativeCache["a1"]; len(cached) == 0 {
		t.Fatal("native cache was not rebuilt")
	}
	idx := r.messageIndexOf(r.sync.MsgByID["a1"])
	item := r.vl.Item(idx)
	item.Native = nil // simulate a future item-list invariant violation
	_ = r.vl.ExtractVisibleNative()
	if len(item.Native) != len(item.Rendered) || styledSpanCount(item.Native) != base {
		t.Fatalf("visible native repair lost styling: native=%d text=%d styled=%d want=%d",
			len(item.Native), len(item.Rendered), styledSpanCount(item.Native), base)
	}

	// While a1 is the live streaming cursor its body intentionally gains the
	// cursor row (and the progressive stream path may differ), so the stream
	// frame is recorded separately instead of compared to base.
	count("turn1-stream-on", "a1")
	r.SetStreaming(false)
	if got := count("turn1-stream-off", "a1"); got != base {
		t.Errorf("turn1 stream-off: styled span count %d != base %d", got, base)
		dumpItemRows(t, "stream-off", r)
	}

	// ---- Turn 2 ----
	r.SyncMessages([]types.ChatMessage{
		{InternalID: "u1", Role: types.RoleUser, Content: "write me some go", Time: time.Unix(1, 0)},
		{InternalID: "a1", Role: types.RoleAssistant, Content: markdownBody, Time: time.Unix(2, 0)},
		{InternalID: "s2", Role: types.RoleStatus, Content: "agent turn started", Time: time.Unix(3, 0)},
	}, 3)
	r.SyncMessages([]types.ChatMessage{
		{InternalID: "u1", Role: types.RoleUser, Content: "write me some go", Time: time.Unix(1, 0)},
		{InternalID: "a1", Role: types.RoleAssistant, Content: markdownBody, Time: time.Unix(2, 0)},
		{InternalID: "s2", Role: types.RoleStatus, Content: "agent turn started", Time: time.Unix(3, 0)},
		{InternalID: "u2", Role: types.RoleUser, Content: "thanks, more", Time: time.Unix(4, 0)},
	}, 4)
	if got := count("turn2-append", "a1"); got != base {
		t.Errorf("turn2 after append: styled span count %d != base %d", got, base)
	}

	// Stream the second assistant in.
	for step := 0; step < 5; step++ {
		content := markdownBody
		for range step {
			content += "\nmore tokens per chunk"
		}
		r.SyncMessages([]types.ChatMessage{
			{InternalID: "u1", Role: types.RoleUser, Content: "write me some go", Time: time.Unix(1, 0)},
			{InternalID: "a1", Role: types.RoleAssistant, Content: markdownBody, Time: time.Unix(2, 0)},
			{InternalID: "s2", Role: types.RoleStatus, Content: "agent turn started", Time: time.Unix(3, 0)},
			{InternalID: "u2", Role: types.RoleUser, Content: "write me some go", Time: time.Unix(4, 0)},
			{InternalID: "a2", Role: types.RoleAssistant, Content: "thinking out loud..." + fmt.Sprint(step), Time: time.Unix(5, 0)},
		}, uint64(5+step))
		r.SetStreaming(step < 4)
		_ = r.Render(120)
		r.SetSpinFrame(step)
		_ = r.Render(120)
		if got := count(fmt.Sprintf("turn2-step%d", step), "a1"); got != base {
			t.Errorf("turn2 step %d: msg0 styled span count %d != base %d", step, got, base)
		}
	}
	r.SetStreaming(false)
	if got := count("turn2-final", "a1"); got != base {
		t.Errorf("turn2 final: msg0 styled span count %d != base %d", got, base)
	}

	// Toast an idle tick churn: parity checks.
	for i := 0; i < 10; i++ {
		r.InvalidateCache()
		_ = r.Render(120)
		if got := count(fmt.Sprintf("idle%d", i), "a1"); got != base {
			t.Errorf("idle tick %d: msg0 styled span count %d != base %d", i, got, base)
		}
	}
}
