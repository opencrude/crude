package session

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/agent/collaboration"
	"crude/agent/memory"
	rootprompt "crude/agent/prompt"
	"crude/agent/provider"
	"crude/internal/providersim"
)

// A user told agents in four sessions that Caps Lock and Esc are swapped on
// their keyboard. Each time it arrived as steering while a turn ran, and each
// steering message was stored at turn 0. The turn mirror reads a turn by its
// number, so no curator request, history search or memory ever saw it. In
// that workspace 233 of 533 human messages were steering.
func TestHumanSteeringReachesMemoryCurator(t *testing.T) {
	const statement = "Caps Lock and Esc are swapped on my keyboard. Use Caps Lock when a capture needs Esc."
	proposal, err := json.Marshal(memory.ExtractionResult{Memories: []memory.ExtractedMemory{{
		Kind: memory.KindFact, Title: "Caps Lock and Esc are swapped", Content: statement, Importance: 50, Evidence: []int{1},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var curatorInput string
	gate := providersim.NewGate()
	t.Cleanup(gate.Release)
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		if system := simText(request.System); strings.Contains(system, "memory curator") {
			var input strings.Builder
			for _, message := range request.Messages {
				input.WriteString(simText(message.Parts))
			}
			mu.Lock()
			curatorInput = input.String()
			mu.Unlock()
			return providersim.Text(string(proposal))
		}
		if request.Index == 1 {
			return providersim.Text("Started the capture.").WithGate(gate)
		}
		return providersim.Text("Noted. I will use Caps Lock for Esc.")
	})
	cfg := modelAttributionConfig(server.BaseURL(providersim.Chat), "steering-memory-model")
	cfg.Memory.Consolidation = "off"
	home, project := t.TempDir(), t.TempDir()
	s, err := NewWithSessionDir("", cfg, home, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(project)
	memories := newTestMemoryStore(t)
	s.SetMemoryStore(memories)
	// The opening request matches the first memory; only the steering text
	// matches the second.
	if _, err := memories.Remember(t.Context(), memory.Record{Kind: memory.KindFact, Title: "Settings window layout",
		Content: "The settings window uses the shared form controls.", Importance: 50, Source: memory.SourceTool}); err != nil {
		t.Fatal(err)
	}
	known, err := memories.Remember(t.Context(), memory.Record{Kind: memory.KindFact, Title: "Keyboard swap for tuishot",
		Content: "Send key:Caps_Lock in scripts/tuishot.sh when the keyboard swaps Caps Lock and Esc.", Importance: 50, Source: memory.SourceTool})
	if err != nil {
		t.Fatal(err)
	}
	journal, err := collaboration.Attach(s.db, project, filepath.Join(home, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	s.SetCollaboration(journal, "steering-memory")
	sender := newMockEventSender()
	o, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !o.SubmitInput(provider.PlainInput("Capture the settings window.")) {
		t.Fatal("turn was rejected")
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(server.Requests()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	participant, err := journal.Agent(t.Context(), o.ID)
	if err != nil || participant.CurrentRunID == "" {
		t.Fatalf("no active run to steer: %+v err=%v", participant, err)
	}
	if _, err := journal.Publish(t.Context(), collaboration.PublishRequest{
		SenderID: "human", SenderName: "Human", Origin: collaboration.OriginHuman,
		Kind: collaboration.EventHumanSteering, IdempotencyKey: "keyboard", RecipientIDs: []string{o.ID},
		RunID: participant.CurrentRunID, Body: statement,
	}); err != nil {
		t.Fatal(err)
	}
	gate.Release()
	waitTurnDone(t, sender, o.ID)
	if len(server.Requests()) != 2 {
		t.Fatalf("steering did not reach the agent: %d requests", len(server.Requests()))
	}

	var turn int
	if err := s.db.QueryRow(`SELECT turn_number FROM messages WHERE origin='human_steering'`).Scan(&turn); err != nil || turn != 1 {
		t.Fatalf("steering is stored at turn %d (err=%v), want the turn it steered", turn, err)
	}
	turns, err := memories.TurnsForSession(t.Context(), s.sessionID, 0, 10)
	if err != nil || len(turns) != 1 || !strings.Contains(turns[0].UserText, "Capture the settings window.") || !strings.Contains(turns[0].UserText, statement) {
		t.Fatalf("turn mirror lost the steering: %+v err=%v", turns, err)
	}
	observations, err := memories.ObservationsForSession(t.Context(), s.sessionID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	stated := false
	for _, observation := range observations {
		stated = stated || observation.Kind == memory.EvidenceUser && observation.Summary == statement
	}
	if !stated {
		t.Fatalf("no human statement carries the steering text: %+v", observations)
	}
	hits, _, err := memories.SearchHistory(t.Context(), "keyboard Caps Lock", 5)
	if err != nil || len(hits) == 0 || hits[0].Turn != 1 {
		t.Fatalf("history search cannot find the steering: %+v err=%v", hits, err)
	}

	// The steering recalled what the workspace knows about it, once.
	exposures, err := memories.ExposuresForMemory(t.Context(), known.ID, 4)
	if err != nil || len(exposures) != 1 || exposures[0].Kind != "automatic" {
		t.Fatalf("steering did not recall the related memory: %+v err=%v", exposures, err)
	}
	s.msgsMu.Lock()
	active := append([]provider.ChatMessage(nil), s.activeContext...)
	s.msgsMu.Unlock()
	if !shownMemories(active)[memory.RevisionRef{ID: known.ID, Revision: known.Revision}] {
		t.Fatal("the recalled memory is not in the agent's context")
	}
	// The ranker reads the human's words, not the delivery instructions that
	// fill the first bytes of the steering prompt.
	batch := []provider.ChatMessage{
		{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "read", Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"notes.txt"}`}}}},
		{Role: "tool", ToolCallID: "read", Content: provider.TextContent("notes")},
	}
	if state, _ := actionRankingState(active, batch, project, s.typedText); state.Task != statement {
		t.Fatalf("ranker task = %q, want the steering text", state.Task)
	}

	s.runExtraction(t.Context(), s.sessionID, false)
	mu.Lock()
	input := curatorInput
	mu.Unlock()
	if !strings.Contains(input, statement) || strings.Contains(input, rootprompt.HumanSteeringPrefix()) {
		t.Fatalf("curator did not receive what the human typed:\n%s", input)
	}
	all, err := memories.List(t.Context(), memory.ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var saved *memory.ScoredRecord
	for i := range all {
		if all[i].Title == "Caps Lock and Esc are swapped" {
			saved = &all[i]
		}
	}
	if saved == nil || len(saved.Evidence) != 1 || saved.Evidence[0].Kind != memory.EvidenceUser || saved.Evidence[0].Summary != statement {
		t.Fatalf("the stated fact is not a memory grounded in the human's words: %+v", all)
	}
	recalled, err := memories.Recall(t.Context(), memory.RecallQuery{Text: "Close the dialog with Esc in the capture", TouchedPaths: []string{"agent/session/run.go"}, Limit: 5})
	if err != nil || !slices.ContainsFunc(recalled, func(r memory.ScoredRecord) bool { return r.ID == saved.ID }) {
		t.Fatalf("a capture that needs Esc does not recall the keyboard fact: %+v err=%v", recalled, err)
	}
	s.finishMemoryJobs()
}
