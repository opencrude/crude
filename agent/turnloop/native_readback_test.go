package turnloop

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"crude/agent/provider"
	"crude/internal/providersim"
)

var statedCheckpointSpace = regexp.MustCompile(`at most (\d+) estimated tokens`)

// readBack is one plain request that reads the provider checkpoint back.
type readBack struct {
	stated, limit int
	stream        bool
}

// compactWithReadBacks runs an automatic checkpoint of a context the serving
// model wrote, on a Responses route whose native operation works. answer decides each read-back reply; a portable
// fallback gets the handoff unchanged.
func compactWithReadBacks(t *testing.T, account, handoff string, answer func(index int, r readBack) providersim.Turn) (CompactResult, []readBack, []string, int) {
	t.Helper()
	const model = "fixture-native"
	sim := providersim.New(t)
	var reads []readBack
	sim.Respond(func(r providersim.Request) providersim.Turn {
		if strings.HasSuffix(r.Path, "/compact") {
			return providersim.Turn{}
		}
		if bytes.Contains(r.Raw, []byte("old assistant evidence")) {
			// A portable fallback reads the whole source.
			return providersim.Text(handoff)
		}
		text := conversationText(r.Effective)
		m := statedCheckpointSpace.FindStringSubmatch(text)
		if m == nil {
			return providersim.Text(probeTicket(text))
		}
		var read readBack
		read.stated, _ = strconv.Atoi(m[1])
		limit, _ := r.OutputLimit.Int64()
		read.limit = int(limit)
		read.stream = r.Stream
		reads = append(reads, read)
		return answer(len(reads)-1, read)
	})
	p := &provider.Provider{ID: "custom", AccountID: account, StandardName: "openai-responses", Model: model, BaseURL: sim.BaseURL(providersim.Responses)}
	p.SetContextWindow(1_000_000)
	a := newTestAgent(t, p, model)
	a.NativeCompactionProvider = p
	var history []provider.ChatMessage
	for i := range 12 {
		history = append(history, provider.NewTextMessage("user", "step "+strconv.Itoa(i)), provider.NewTextMessage("assistant", strings.Repeat("old assistant evidence ", 2500)))
	}
	history = append(servedBy(p, history), provider.NewTextMessage("user", "Continue the check."))
	var notices []string
	result, err := a.CompactWithRequestID(t.Context(), history, "system", "", "automatic", func(e AgentEvent) {
		if e.Type == "compaction_route_notice" {
			reason, _ := e.Data.(map[string]any)["reason"].(string)
			notices = append(notices, reason)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return result, reads, notices, len(sim.Requests())
}

func carriesProviderCheckpoint(result CompactResult, text string) bool {
	for _, m := range result.Messages {
		if m.Origin == provider.OriginCheckpoint && len(m.ProviderItems) != 0 && strings.Contains(m.ContentString(), text) {
			return true
		}
	}
	return false
}

// Reading a provider checkpoint back is a normal request of the serving model.
// On 2026-11-25 a /compact of an 817K-token session on a Responses
// route lost its paid native result: the read-back was capped at 8,192 output
// tokens while its instructions offered the whole continuation space (hundreds
// of thousands of tokens) for the checkpoint.
// The reply stopped at the limit, and Crude paid another model to read the
// whole session again.
func TestNativeCheckpointReadBackUsesTheServingAllowance(t *testing.T) {
	handoff := "<<<intent>>>\n" + strings.Repeat("Keep every audit record and keep the build passing. ", 1000) +
		"\n<<<work>>>\nChecked inputs\n<<<state>>>\nTarget is blue\n<<<continuation>>>\nCheck the target"
	need := provider.EstimateTokens(handoff, "fixture-native")
	if need <= 8192 {
		t.Fatalf("fixture checkpoint is %d tokens; it must exceed the old 8,192-token cap", need)
	}
	result, reads, notices, requests := compactWithReadBacks(t, "native-read-back", handoff, func(_ int, r readBack) providersim.Turn {
		if need > r.limit {
			return providersim.Text(handoff[:r.limit]).WithFinish(providersim.FinishLength)
		}
		return providersim.Text(handoff)
	})
	allowance := NewAgent(nil, "fixture-native", nil).outputTokenLimit(1_000_000, 0)
	if len(reads) == 0 || reads[0].limit != allowance || reads[0].limit <= 8192 {
		t.Fatalf("read-backs %+v, want the serving model's normal allowance %d", reads, allowance)
	}
	if !reads[0].stream {
		t.Fatal("a long read-back must stream so the connection shows progress")
	}
	if reads[0].stated <= 0 || reads[0].stated > reads[0].limit {
		t.Fatalf("instructions offered %d tokens of checkpoint space for a %d-token reply", reads[0].stated, reads[0].limit)
	}
	if len(notices) != 0 || requests != 4 || !carriesProviderCheckpoint(result, "Target is blue") {
		t.Fatalf("native checkpoint was not used: notices=%q requests=%d", notices, requests)
	}
}

// A read-back that still stops at its output limit asks the same paid window
// for a shorter checkpoint before Crude gives up the native result.
func TestNativeCheckpointReadBackAtItsLimitAsksForLess(t *testing.T) {
	const handoff = "<<<intent>>>\nKeep the build passing\n<<<work>>>\nChecked inputs\n<<<state>>>\nTarget is blue\n<<<continuation>>>\nCheck the target"
	result, reads, notices, requests := compactWithReadBacks(t, "native-read-back-shorter", handoff, func(index int, r readBack) providersim.Turn {
		if index == 0 {
			return providersim.Text("<<<intent>>>\nKeep").WithFinish(providersim.FinishLength)
		}
		return providersim.Text(handoff)
	})
	if len(reads) != 2 || reads[1].stated != max(1, reads[0].stated/2) || reads[1].limit != reads[0].limit {
		t.Fatalf("read-backs %+v, want a second read with half the checkpoint space", reads)
	}
	if len(notices) != 0 || requests != 5 || !carriesProviderCheckpoint(result, "Target is blue") {
		t.Fatalf("native checkpoint was not used: notices=%q requests=%d", notices, requests)
	}
}

// conversationText joins the text the simulated model sees, in wire order.
func conversationText(c providersim.Conversation) string {
	var b strings.Builder
	for _, part := range c.System {
		b.WriteString(part.Text)
		b.WriteByte('\n')
	}
	for _, m := range c.Messages {
		for _, part := range m.Parts {
			b.WriteString(part.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
